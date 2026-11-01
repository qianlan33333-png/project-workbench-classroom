package workbench

import (
	"github.com/gin-gonic/gin"
	"html/template"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
)

type browserEndpoint struct{ Address, Project string }
type browserLink struct{ Project, URL string }

var browserPicker = template.Must(template.New("browsers").Parse(`<!doctype html><html lang="zh"><meta charset="utf-8"><title>浏览器接管</title><h1>选择要接管的项目</h1><ul>{{range .}}<li><a href="{{.URL}}">{{.Project}}</a></li>{{end}}</ul></html>`))

func (a *API) projectBrowser(c *gin.Context) {
	path := strings.TrimPrefix(c.Param("path"), "/")
	c.Header("Cache-Control", "no-store")
	if path == "" || path == "vnc.html" {
		links := []browserLink{}
		base := strings.TrimRight(a.Config.BasePath, "/") + "/api/v1/browser/run/"
		a.Store.browsers.Range(func(key, value any) bool {
			prefix := base + key.(string) + "/"
			q := c.Request.URL.Query()
			q.Set("path", strings.TrimPrefix(prefix, "/")+"websockify")
			links = append(links, browserLink{value.(browserEndpoint).Project, prefix + "vnc.html?" + q.Encode()})
			return true
		})
		switch len(links) {
		case 0:
			c.String(http.StatusConflict, "项目浏览器尚未就绪。请在任务运行后打开。")
		case 1:
			c.Redirect(http.StatusTemporaryRedirect, links[0].URL)
		default:
			sort.Slice(links, func(i, j int) bool { return links[i].Project < links[j].Project })
			c.Header("Content-Type", "text/html; charset=utf-8")
			_ = browserPicker.Execute(c.Writer, links)
		}
		return
	}
	tail, scoped := strings.CutPrefix(path, "run/")
	run, asset, ok := strings.Cut(tail, "/")
	if !scoped || !ok {
		c.String(409, "请从浏览器接管入口选择当前任务。")
		return
	}
	endpoint, live := a.Store.browsers.Load(run)
	if !live {
		c.String(410, "这次任务的浏览器已经关闭。")
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: endpoint.(browserEndpoint).Address})
	direct := proxy.Director
	proxy.Director = func(req *http.Request) {
		direct(req)
		req.URL.Path = "/" + asset
		req.URL.RawPath = ""
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "浏览器正在启动或已关闭。", 503)
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}
