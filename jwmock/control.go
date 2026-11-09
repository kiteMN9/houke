package jwmock

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleControl 是 /__mock/ 控制面。
//
// 控制面不挂在 BasePath 下，也不参与故障注入和请求流水，方便测试里随时改
// 场景而不影响“业务请求”的计数。
func (s *Server) handleControl(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, controlPrefix)
	switch rel {
	case "/state":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		writeJSON(w, http.StatusOK, s.Snapshot())

	case "/requests":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		writeJSON(w, http.StatusOK, s.Snapshot().Requests)

	case "/reset":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		s.Reset()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/scenario":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		var sc Scenario
		if err := json.NewDecoder(r.Body).Decode(&sc); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if err := s.SetScenario(sc); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/revoke":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"revoked": s.RevokeSessions(),
		})

	default:
		http.NotFound(w, r)
	}
}

// methodNotAllowed 返回 405。
func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
}
