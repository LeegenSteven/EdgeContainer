package edgestack

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

var inlineScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

func (s *Service) voiceTask(r *http.Request) (Task, error) {
	var task Task
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		task, e = s.authorizedTask(r, tx, mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		if task.VoiceOrigin == "" || !contains(task.Capabilities, "voice-test") {
			return fail(404, "voice_test_unavailable")
		}
		return nil
	})
	return task, err
}
func (s *Service) voiceTest(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.voiceTask(r); err != nil {
		return err
	}
	if len(s.opts.VoiceTestHTML) == 0 {
		return fail(503, "voice_test_page_unavailable")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Permissions-Policy", "microphone=(self)")
	scripts := []string{"'self'"}
	for _, match := range inlineScript.FindAllSubmatch(s.opts.VoiceTestHTML, -1) {
		sum := sha256.Sum256(match[1])
		scripts = append(scripts, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src "+strings.Join(scripts, " ")+"; style-src 'self' 'unsafe-inline'; media-src 'self' blob:; connect-src 'self' wss://"+r.Host+" ws://"+r.Host+"; object-src 'none'; frame-ancestors 'none'")
	_, err := w.Write(s.opts.VoiceTestHTML)
	return err
}
func (s *Service) voiceProxy(w http.ResponseWriter, r *http.Request) error {
	task, err := s.voiceTask(r)
	if err != nil {
		return err
	}
	path := "/" + mux.Vars(r)["path"]
	if !contains([]string{"/v2v/stream", "/asr/stream", "/health", "/model-info"}, path) {
		return fail(404, "voice_path_not_allowed")
	}
	// Browser WebSockets carry the existing HttpOnly session cookie. Check
	// Origin before upgrading so another website cannot reuse that cookie.
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		origin, e := url.Parse(r.Header.Get("Origin"))
		if e != nil || origin.Host != r.Host || (origin.Scheme != "http" && origin.Scheme != "https") {
			return fail(403, "voice_origin_denied")
		}
	}
	upstream, err := url.Parse(task.VoiceOrigin)
	if err != nil {
		return fail(503, "voice_upstream_unavailable")
	}
	token, err := voiceToken(task.VoiceTokenFile)
	if err != nil {
		return fail(503, "voice_upstream_credentials_unavailable")
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	director := proxy.Director
	proxy.Director = func(out *http.Request) {
		director(out)
		out.URL.Path = path
		out.URL.RawPath = ""
		out.Host = upstream.Host
		out.Header.Del("Cookie")
		out.Header.Del("Authorization")
		out.Header.Del("X-API-Key")
		out.Header.Del("X-PortainerAgent-EdgeID")
		out.Header.Del("X-PortainerAgent-Signature")
		out.Header.Set("Authorization", "Bearer "+token)
		out.Header.Set("Origin", upstream.Scheme+"://"+upstream.Host)
	}
	proxy.ModifyResponse = func(res *http.Response) error {
		res.Header.Del("Set-Cookie")
		res.Header.Del("Access-Control-Allow-Origin")
		res.Header.Set("Cache-Control", "no-store")
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		respond(w, 502, map[string]string{"errorCode": "voice_upstream_unavailable"})
	}
	proxy.ServeHTTP(w, r)
	return nil
}

func voiceToken(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", http.ErrNotSupported
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return "", http.ErrNotSupported
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", http.ErrNotSupported
	}
	return token, nil
}
