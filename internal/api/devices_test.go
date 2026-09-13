package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ider-zh/lanwatch/internal/store"
)

func TestFixedServerMarkAndFilter(t *testing.T) {
	a, cleanup := newTestAPI(t)
	defer cleanup()
	const server = "AA:00:00:00:00:01"
	const client = "AA:00:00:00:00:02"
	const offline = "AA:00:00:00:00:03"
	for _, mac := range []string{server, client, offline} {
		if err := a.st.UpsertDevice(store.Device{MAC: mac, Name: "device", Status: "tracked"}); err != nil {
			t.Fatal(err)
		}
		if mac != offline {
			if _, err := a.st.OpenSession(mac, time.Now().Unix(), "test"); err != nil {
				t.Fatal(err)
			}
		}
	}
	patch := func(body map[string]any) {
		t.Helper()
		rec := doJSON(a, http.MethodPatch, "/api/devices/"+server, body, a.handlePatchDevice, map[string]string{"mac": server})
		if rec.Code != http.StatusOK {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
		}
	}
	patch(map[string]any{"fixed_server": true})
	// Later observations and unrelated edits must preserve the manual mark.
	if err := a.st.UpsertDevice(store.Device{MAC: server, CurrentIP: "192.168.1.20"}); err != nil {
		t.Fatal(err)
	}
	patch(map[string]any{"name": "server"})
	d, err := a.st.GetDevice(server)
	if err != nil {
		t.Fatal(err)
	}
	if !d.FixedServer || d.Status != "tracked" {
		t.Fatalf("mark/status changed: %+v", d)
	}
	check := func(query string, want int, marked bool) {
		t.Helper()
		rec := doJSON(a, http.MethodGet, "/api/devices"+query, nil, a.handleDevices)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		var result struct{ Devices []store.DeviceView }
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Devices) != want {
			t.Fatalf("%s: got %d devices, want %d", query, len(result.Devices), want)
		}
		for _, d := range result.Devices {
			if d.MAC == server && d.FixedServer != marked {
				t.Fatalf("unexpected mark: %+v", d)
			}
			if query == "?online=true&hide_fixed_servers=true" && d.MAC != client {
				t.Fatalf("unexpected online device: %+v", d)
			}
		}
	}
	check("", 3, true)
	check("?online=true", 2, true)
	check("?hide_fixed_servers=true", 2, true)
	check("?online=true&hide_fixed_servers=true", 1, true)
	check("?hide_fixed_servers=true&q=server", 0, true)
	check("?hide_fixed_servers=false&q=server", 1, true)
	patch(map[string]any{"fixed_server": false})
	check("?hide_fixed_servers=true", 3, false)
}
