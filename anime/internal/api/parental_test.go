package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/history"
	"github.com/v0-0x/mediaserver/anime/internal/share"
)

// A host sets a guest's parental controls: the guest gets them, its Aniko
// hides and refuses what they block (and keeps sharing on), the host shares
// nothing blocked with it, hears what it watches, and can lift them; a PIN
// lifts them on the guest.
func TestParentalControls(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	blockedPath, _ := addEpisode(t, host, 21, 1)
	addEpisode(t, host, 22, 1)
	hostID, guestID := host.app.Share.ID(), guest.app.Share.ID()

	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	eventually(t, "the host to list the guest", func() bool { _, ok := peerIn(host, guest); return ok })

	// Only for a device the library is shared with.
	if w := do(host, "POST", "/api/sharing/peers/"+guestID+"/parental", local, `{"blocked":[21]}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("rules for a device not shared with: %d", w.Code)
	}
	if err := host.app.Share.SetAllowed(guestID, true); err != nil {
		t.Fatal(err)
	}
	// Not from a device on the network.
	if w := do(host, "POST", "/api/sharing/peers/"+guestID+"/parental", "192.168.1.20:5000", `{"blocked":[21]}`, nil); w.Code == http.StatusOK {
		t.Fatal("a device on the network set parental controls")
	}
	if w := do(host, "POST", "/api/sharing/peers/"+guestID+"/parental", local, `{"blocked":[21],"newPin":"4821"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}

	parentalOn := func() bool {
		do(guest, "GET", "/api/sharing?refresh=1", local, "", nil) // asks again
		var st struct {
			Active bool                   `json:"active"`
			From   []share.ParentalSource `json:"from"`
		}
		_ = json.Unmarshal(do(guest, "GET", "/api/parental", local, "", nil).Body.Bytes(), &st)
		return st.Active && len(st.From) == 1 && st.From[0].ID == hostID && st.From[0].HasPIN
	}
	eventually(t, "the guest to get its parental controls", parentalOn)

	// What's shared with it: not the blocked anime.
	var libs []share.SharedLibrary
	eventually(t, "the shared library without the blocked anime", func() bool {
		_ = json.Unmarshal(do(guest, "GET", "/api/sharing/libraries?refresh=1", local, "", nil).Body.Bytes(), &libs)
		return len(libs) == 1 && len(libs[0].Files) == 1 && libs[0].Files[0].MediaID == 22
	})
	// Asked anyway: refused, on the guest and on the host.
	w := do(guest, "GET", "/api/anime/21", local, "", nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"parental":true`) {
		t.Fatalf("a blocked anime's page: %d %s", w.Code, w.Body)
	}
	remote := share.RemotePath(hostID, blockedPath)
	if w := do(guest, "GET", "/api/local/file?"+url.Values{"path": {remote}}.Encode(), local, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("playing a blocked anime's file: %d", w.Code)
	}
	if w := do(guest, "POST", "/api/playback/progress", local, `{"mediaId":21,"episode":1,"position":10,"duration":1420,"source":"local"}`, nil); w.Code != http.StatusForbidden {
		t.Fatalf("a blocked anime's progress: %d", w.Code)
	}
	// Library sharing stays on.
	cfg := guest.app.Settings.Get()
	cfg.Sharing.Enabled = false
	body, _ := json.Marshal(cfg)
	if w := do(guest, "PUT", "/api/settings", local, string(body), nil); w.Code != http.StatusForbidden {
		t.Fatalf("turning sharing off under parental controls: %d", w.Code)
	}

	// What it watches reaches the host.
	if err := guest.app.History.Save(history.Entry{MediaID: 22, Episode: 1, Position: 300, Duration: 1420, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	var seen struct {
		Watched []struct {
			MediaID  int     `json:"mediaId"`
			Position float64 `json:"position"`
		} `json:"watched"`
		HasPin bool `json:"hasPin"`
	}
	deadline := time.Now().Add(12 * time.Second)
	for {
		_ = json.Unmarshal(do(host, "GET", "/api/sharing/peers/"+guestID+"/parental", local, "", nil).Body.Bytes(), &seen)
		if len(seen.Watched) == 1 && seen.Watched[0].MediaID == 22 && seen.Watched[0].Position == 300 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("what the guest watched: %+v", seen)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !seen.HasPin {
		t.Fatal("the PIN wasn't kept")
	}

	// The PIN lifts them on the guest; a wrong one doesn't.
	if w := do(guest, "POST", "/api/parental/unlock", local, `{"pin":"0000"}`, nil); w.Code != http.StatusForbidden {
		t.Fatalf("a wrong PIN: %d", w.Code)
	}
	if w := do(guest, "POST", "/api/parental/unlock", local, `{"pin":"4821"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("the PIN: %d %s", w.Code, w.Body)
	}
	if guest.app.Share.Parental().Active() {
		t.Fatal("still on after the PIN")
	}
	// The host still has them: back with its next answer; then it lifts them.
	eventually(t, "the controls back from the host", parentalOn)
	if w := do(host, "POST", "/api/sharing/peers/"+guestID+"/parental", local, `{}`, nil); w.Code != http.StatusOK {
		t.Fatalf("lift: %d", w.Code)
	}
	eventually(t, "the controls lifted", func() bool {
		do(guest, "GET", "/api/sharing?refresh=1", local, "", nil)
		return !guest.app.Share.Parental().Active()
	})
	if w := do(guest, "GET", "/api/anime/21", local, "", nil); w.Code == http.StatusForbidden {
		t.Fatal("still blocked after they were lifted")
	}
}
