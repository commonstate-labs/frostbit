package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

                                                      
type emergencyStatusResp struct {
	OK        bool   `json:"ok"`
	Stopped   bool   `json:"stopped"`
	Reason    string `json:"reason,omitempty"`
	StoppedAt string `json:"stopped_at,omitempty"`
}

                                                                                                      
func (hm *HandlerManager) checkAdminToken(r *http.Request) bool {
	if hm.cfg == nil {
		return false
	}
	want := hm.cfg.Server.AdminToken
	if want == "" {
		return false                                               
	}
	got := r.Header.Get("X-Admin-Token")
	if got == "" {
		got = r.URL.Query().Get("admin_token")
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

                                           
                                
                                                 
func (hm *HandlerManager) HandleEmergencyStop(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleEmergencyStop")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !hm.checkAdminToken(r) {
		http.Error(w, "admin auth required", http.StatusUnauthorized)
		return
	}
	if hm.emergency == nil {
		http.Error(w, "emergency switch not wired", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)                  
	hm.emergency.Stop(body.Reason)
	                                                                                                                      
	if hm.consensusManager != nil {
		hm.consensusManager.SetPaused(true)
	}
	hm.Logger.Warn("[Emergency] STOP engaged; reason=%q", body.Reason)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "stopped": true, "reason": body.Reason})
}

                                               
                                                               
func (hm *HandlerManager) HandleEmergencyResume(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleEmergencyResume")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !hm.checkAdminToken(r) {
		http.Error(w, "admin auth required", http.StatusUnauthorized)
		return
	}
	if hm.emergency == nil {
		http.Error(w, "emergency switch not wired", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !body.Confirm {
		http.Error(w, "resume requires {confirm:true}", http.StatusBadRequest)
		return
	}
	hm.emergency.Resume()
	hm.Logger.Warn("[Emergency] RESUME (admin-confirmed); consensus pause still needs manual /pause toggle")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "stopped": false})
}

                                                                                                     
func (hm *HandlerManager) HandleEmergencyStatus(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleEmergencyStatus")
	resp := emergencyStatusResp{OK: true}
	if hm.emergency != nil {
		resp.Stopped = hm.emergency.IsStopped()
		resp.Reason = hm.emergency.Reason()
		if t := hm.emergency.StoppedAt(); !t.IsZero() {
			resp.StoppedAt = t.UTC().Format("2006-01-02T15:04:05Z")
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
