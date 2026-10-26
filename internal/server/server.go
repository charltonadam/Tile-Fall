// Package server serves the browser client, the WebSocket sync protocol, a
// small REST API and the MCP endpoint, all from one http.Handler.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charltonadam/tilefall/assets"
	"github.com/charltonadam/tilefall/internal/game"
	"github.com/charltonadam/tilefall/internal/mcpserver"
	"github.com/charltonadam/tilefall/internal/render"
	"github.com/charltonadam/tilefall/internal/scene"
	"github.com/charltonadam/tilefall/web"
)

type Options struct {
	// MCPFromLAN allows MCP clients on other machines. By default /mcp only
	// answers loopback connections, since anyone with MCP access is the DM.
	MCPFromLAN bool
}

type Server struct {
	g       *game.Game
	opt     Options
	mux     *http.ServeMux
	handler http.Handler
}

func New(g *game.Game, opt Options) *Server {
	s := &Server{g: g, opt: opt, mux: http.NewServeMux()}
	s.routes()
	// reject cross-site browser requests (a web page the DM visits must not be
	// able to drive the table); non-browser clients such as MCP are unaffected
	s.handler = http.NewCrossOriginProtection().Handler(s.mux)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /ws", s.handleWS)

	m.HandleFunc("GET /api/catalog", s.handleCatalog)
	m.HandleFunc("GET /api/scene", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(s.g.Table.Snapshot())
	})
	m.HandleFunc("POST /api/commands", s.handleCommands)
	m.HandleFunc("POST /api/undo", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.g.Table.Undo()
		reply(w, map[string]uint64{"version": v}, err)
	})
	m.HandleFunc("GET /api/scenes", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.g.ListScenes()
		reply(w, map[string]any{"active": s.g.Table.SceneID(), "scenes": list}, err)
	})
	m.HandleFunc("POST /api/scenes", func(w http.ResponseWriter, r *http.Request) {
		var p game.NewSceneParams
		if err := decodeBody(r, &p); err != nil {
			reply(w, nil, err)
			return
		}
		id, err := s.g.NewScene(p)
		reply(w, map[string]string{"id": id}, err)
	})
	m.HandleFunc("POST /api/scenes/{id}/load", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]string{"active": r.PathValue("id")}, s.g.LoadScene(r.PathValue("id")))
	})
	m.HandleFunc("DELETE /api/scenes/{id}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, s.g.DeleteScene(r.PathValue("id")))
	})
	m.HandleFunc("GET /api/library", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"templates": s.g.Library.List()}, nil)
	})
	m.HandleFunc("POST /api/library", func(w http.ResponseWriter, r *http.Request) {
		var p game.NewTemplateParams
		if err := decodeBody(r, &p); err != nil {
			reply(w, nil, err)
			return
		}
		id, err := s.g.Library.Create(p)
		reply(w, map[string]string{"id": id}, err)
	})
	m.HandleFunc("POST /api/library/{id}", func(w http.ResponseWriter, r *http.Request) {
		var u game.TemplateUpdate
		if err := decodeBody(r, &u); err != nil {
			reply(w, nil, err)
			return
		}
		u.ID = r.PathValue("id")
		reply(w, map[string]bool{"ok": true}, s.g.Library.Update(u))
	})
	m.HandleFunc("DELETE /api/library/{id}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, s.g.Library.Delete(r.PathValue("id")))
	})
	m.HandleFunc("GET /api/preview.png", s.handlePreview)
	m.HandleFunc("POST /api/uploads", s.handleUpload)

	m.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets.FS)))
	m.Handle("GET /uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(s.g.Store.UploadsDir()))))

	m.Handle("/mcp", s.loopbackOnly(mcpserver.Handler(s.g)))

	m.Handle("/", spaHandler(web.Dist()))
}

func (s *Server) loopbackOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.opt.MCPFromLAN {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				http.Error(w, "MCP is only available from the host machine (start with -mcp-lan to allow LAN clients)", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(v)
}

func decodeBody(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(v)
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	reply(w, s.g.Table.Catalog(), nil)
}

// handleCommands accepts a single command object or {"commands": [...]}.
func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		reply(w, nil, err)
		return
	}
	cmds, err := decodeCommands(body)
	if err != nil {
		reply(w, nil, err)
		return
	}
	res, err := s.g.ApplyBatch(cmds)
	reply(w, res, err)
}

func decodeCommands(body []byte) ([]scene.Command, error) {
	var batch struct {
		Commands []json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal(body, &batch); err == nil && batch.Commands != nil {
		cmds := make([]scene.Command, len(batch.Commands))
		for i, raw := range batch.Commands {
			c, err := scene.DecodeCommand(raw)
			if err != nil {
				return nil, fmt.Errorf("commands[%d]: %w", i, err)
			}
			cmds[i] = c
		}
		return cmds, nil
	}
	c, err := scene.DecodeCommand(body)
	if err != nil {
		return nil, err
	}
	return []scene.Command{c}, nil
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	px, _ := strconv.ParseFloat(r.URL.Query().Get("px"), 64)
	var data []byte
	var err error
	s.g.Table.Read(func(sc *scene.Scene) {
		data, err = render.PNG(sc, s.g.Table.Catalog(), render.Options{PxPerCell: px, Labels: r.URL.Query().Get("labels") != "0"})
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(data)
}

var uploadExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".svg": true}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer f.Close()
	ext := strings.ToLower(filepath.Ext(hdr.Filename))
	if !uploadExts[ext] {
		reply(w, nil, errors.New("only png, jpg, webp, gif and svg images can be uploaded"))
		return
	}
	b := make([]byte, 8)
	rand.Read(b)
	name := hex.EncodeToString(b) + ext
	out, err := os.Create(filepath.Join(s.g.Store.UploadsDir(), name))
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, f); err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, map[string]string{"url": "/uploads/" + name}, nil)
}

// spaHandler serves the built frontend, falling back to index.html for
// unknown paths. If the frontend has not been built it explains how.
func spaHandler(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(dist, "index.html"); err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "The frontend has not been built. Run `npm run build` in web/ and restart, or use the Vite dev server (`npm run dev` in web/).")
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if _, err := fs.Stat(dist, p); err != nil {
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}
