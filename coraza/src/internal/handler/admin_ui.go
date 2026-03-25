package handler

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"mamotama/internal/config"
)

//go:embed admin_ui_dist
var adminUIEmbedFS embed.FS

func RegisterAdminUIRoutes(r *gin.Engine) {
	if r == nil {
		return
	}
	uiFS, err := fs.Sub(adminUIEmbedFS, "admin_ui_dist")
	if err != nil {
		return
	}
	base := strings.TrimSpace(config.UIBasePath)
	if base == "" {
		base = "/mamotama-ui"
	}
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}

	serveFile := func(c *gin.Context, relPath string) {
		if relPath == "" {
			relPath = "index.html"
		}
		relPath = strings.TrimPrefix(path.Clean("/"+relPath), "/")
		if relPath == "." {
			relPath = "index.html"
		}

		if relPath != "index.html" {
			if _, err := fs.Stat(uiFS, relPath); err != nil {
				relPath = "index.html"
			}
		}

		raw, err := fs.ReadFile(uiFS, relPath)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		ct := mime.TypeByExtension(path.Ext(relPath))
		if ct == "" {
			ct = http.DetectContentType(raw)
		}
		c.Data(http.StatusOK, ct, raw)
	}

	r.GET(base, func(c *gin.Context) {
		serveFile(c, "index.html")
	})
	r.HEAD(base, func(c *gin.Context) {
		serveFile(c, "index.html")
	})
	r.GET(base+"/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		serveFile(c, p)
	})
	r.HEAD(base+"/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		serveFile(c, p)
	})
}
