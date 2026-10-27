// Package mockbmc is a tiny fake Redfish service for tests and demos.
package mockbmc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Credentials accepted by the mock.
const (
	User = "admin"
	Pass = "password"
)

var tree = map[string]string{
	"/redfish/v1":                            `{"@odata.id":"/redfish/v1","@odata.type":"#ServiceRoot.v1_9_0.ServiceRoot","Id":"RootService","Name":"Mock Service Root","RedfishVersion":"1.11.0","Product":"mockbmc","Systems":{"@odata.id":"/redfish/v1/Systems"},"Chassis":{"@odata.id":"/redfish/v1/Chassis"},"Managers":{"@odata.id":"/redfish/v1/Managers"},"SessionService":{"@odata.id":"/redfish/v1/SessionService"},"Links":{"Sessions":{"@odata.id":"/redfish/v1/SessionService/Sessions"}}}`,
	"/redfish/v1/Systems":                    `{"@odata.id":"/redfish/v1/Systems","@odata.type":"#ComputerSystemCollection.ComputerSystemCollection","Name":"Computer System Collection","Members@odata.count":2,"Members":[{"@odata.id":"/redfish/v1/Systems/1"},{"@odata.id":"/redfish/v1/Systems/2"}]}`,
	"/redfish/v1/Systems/1":                  `{"@odata.id":"/redfish/v1/Systems/1","@odata.type":"#ComputerSystem.v1_13_0.ComputerSystem","Id":"1","Name":"Mock System One","Description":"A pretend server with a long description that should wrap nicely inside the detail pane when the terminal is narrow.","Manufacturer":"Acme","Model":"Rack 9000","SerialNumber":"SN0001","PowerState":"On","Status":{"State":"Enabled","Health":"OK","HealthRollup":"Warning"},"MemorySummary":{"TotalSystemMemoryGiB":256,"Status":{"Health":"OK"}},"ProcessorSummary":{"Count":2,"Model":"Acme CPU","Status":{"Health":"OK"}},"Boot":{"BootSourceOverrideEnabled":"Once","BootSourceOverrideTarget":"Pxe","BootSourceOverrideTarget@Redfish.AllowableValues":["None","Pxe","Hdd","Cd","BiosSetup"]},"UUID":"7d1a0bdc-5b9b-4f6c-9a5e-123456789abc","Processors":{"@odata.id":"/redfish/v1/Systems/1/Processors"},"Actions":{"#ComputerSystem.Reset":{"target":"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset","@Redfish.ActionInfo":"/redfish/v1/Systems/1/ResetActionInfo","ResetType@Redfish.AllowableValues":["On","ForceOff","GracefulRestart"]},"Oem":{"Acme":{"#Acme.Beep":{"target":"/redfish/v1/Systems/1/Actions/Oem/Acme.Beep","@Redfish.ActionInfo":"/redfish/v1/Systems/1/BeepActionInfo"}}}},"Links":{"Chassis":[{"@odata.id":"/redfish/v1/Chassis/1"}],"ManagedBy":[{"@odata.id":"/redfish/v1/Managers/BMC"}]},"Oem":{"Acme":{"Widgets":[],"Thing":null,"Nested":{"Deep":{"Deeper":{"Value":true}}}}}}`,
	"/redfish/v1/Systems/1/ResetActionInfo":  `{"@odata.id":"/redfish/v1/Systems/1/ResetActionInfo","@odata.type":"#ActionInfo.v1_1_2.ActionInfo","Id":"ResetActionInfo","Name":"Reset Action Info","Parameters":[{"Name":"ResetType","Required":true,"DataType":"String","AllowableValues":["On","ForceOff","GracefulRestart","ForceRestart"]}]}`,
	"/redfish/v1/Systems/1/BeepActionInfo":   `{"@odata.id":"/redfish/v1/Systems/1/BeepActionInfo","@odata.type":"#ActionInfo.v1_1_2.ActionInfo","Id":"BeepActionInfo","Name":"Beep Action Info","Parameters":[{"Name":"Times","Required":true,"DataType":"Number"},{"Name":"Message","Required":false,"DataType":"String"},{"Name":"Loud","Required":false,"DataType":"Boolean"},{"Name":"Tones","Required":false,"DataType":"StringArray","AllowableValues":["Low","Mid","High"]}]}`,
	"/redfish/v1/Systems/2":                  `{"@odata.id":"/redfish/v1/Systems/2","@odata.type":"#ComputerSystem.v1_13_0.ComputerSystem","Id":"2","Name":"Mock System Two","PowerState":"Off","Status":{"State":"Disabled","Health":"Critical"},"Links":{"Chassis":[{"@odata.id":"/redfish/v1/Chassis/1"}]}}`,
	"/redfish/v1/Systems/1/Processors":       `{"@odata.id":"/redfish/v1/Systems/1/Processors","@odata.type":"#ProcessorCollection.ProcessorCollection","Name":"Processors","Members@odata.count":2,"Members":[{"@odata.id":"/redfish/v1/Systems/1/Processors/CPU.1"},{"@odata.id":"/redfish/v1/Systems/1/Processors/CPU.2"}]}`,
	"/redfish/v1/Systems/1/Processors/CPU.1": `{"@odata.id":"/redfish/v1/Systems/1/Processors/CPU.1","@odata.type":"#Processor.v1_10_0.Processor","Id":"CPU.1","Name":"CPU 1","Socket":"CPU.1","TotalCores":32,"TotalThreads":64,"MaxSpeedMHz":3500,"Status":{"State":"Enabled","Health":"OK"}}`,
	"/redfish/v1/Systems/1/Processors/CPU.2": `{"@odata.id":"/redfish/v1/Systems/1/Processors/CPU.2","@odata.type":"#Processor.v1_10_0.Processor","Id":"CPU.2","Name":"CPU 2","Socket":"CPU.2","TotalCores":32,"TotalThreads":64,"MaxSpeedMHz":3500,"Status":{"State":"Absent","Health":null}}`,
	"/redfish/v1/Chassis":                    `{"@odata.id":"/redfish/v1/Chassis","@odata.type":"#ChassisCollection.ChassisCollection","Name":"Chassis Collection","Members":[{"@odata.id":"/redfish/v1/Chassis/1"}]}`,
	"/redfish/v1/Chassis/1":                  `{"@odata.id":"/redfish/v1/Chassis/1","@odata.type":"#Chassis.v1_14_0.Chassis","Id":"1","Name":"Main Chassis","ChassisType":"RackMount","Status":{"State":"Enabled","Health":"OK"},"Thermal":{"@odata.id":"/redfish/v1/Chassis/1/Thermal"},"Links":{"ComputerSystems":[{"@odata.id":"/redfish/v1/Systems/1"}]}}`,
	"/redfish/v1/Chassis/1/Thermal":          `{"@odata.id":"/redfish/v1/Chassis/1/Thermal","@odata.type":"#Thermal.v1_6_0.Thermal","Name":"Thermal","Temperatures":[{"@odata.id":"/redfish/v1/Chassis/1/Thermal#/Temperatures/0","Name":"CPU1 Temp","ReadingCelsius":54,"UpperThresholdCritical":95,"Status":{"Health":"OK"}},{"@odata.id":"/redfish/v1/Chassis/1/Thermal#/Temperatures/1","Name":"Inlet Temp","ReadingCelsius":31,"Status":{"Health":"Warning"}}],"Fans":[{"Name":"Fan1","Reading":4200,"ReadingUnits":"RPM","Status":{"Health":"OK"}}]}`,
	"/redfish/v1/Managers":                   `{"@odata.id":"/redfish/v1/Managers","@odata.type":"#ManagerCollection.ManagerCollection","Name":"Managers","Members":[{"@odata.id":"/redfish/v1/Managers/BMC"}]}`,
	"/redfish/v1/Managers/BMC":               `{"@odata.id":"/redfish/v1/Managers/BMC","@odata.type":"#Manager.v1_11_0.Manager","Id":"BMC","Name":"Mock BMC","FirmwareVersion":"1.2.3","Status":{"State":"Enabled","Health":"OK"},"Links":{"ManagerForServers":[{"@odata.id":"/redfish/v1/Systems/1"}]}}`,
	"/redfish/v1/SessionService":             `{"@odata.id":"/redfish/v1/SessionService","@odata.type":"#SessionService.v1_1_8.SessionService","Name":"Session Service","ServiceEnabled":true,"SessionTimeout":1800,"Sessions":{"@odata.id":"/redfish/v1/SessionService/Sessions"}}`,
	"/redfish/v1/SessionService/Sessions":    `{"@odata.id":"/redfish/v1/SessionService/Sessions","@odata.type":"#SessionCollection.SessionCollection","Name":"Sessions","Members":[]}`,
}

var (
	mu       sync.Mutex
	sessions = map[string]bool{}
)

// Handler returns the fake Redfish service. Credentials are admin / password.
// With noSession set, session creation 404s so clients must fall back to basic auth.
func Handler(noSession bool, logf func(string, ...any)) http.Handler {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	mux := http.NewServeMux()
	user, pass := User, Pass
	state := map[string]string{}
	for k, v := range tree {
		state[k] = v
	}

	authed := func(r *http.Request) bool {
		if t := r.Header.Get("X-Auth-Token"); t != "" {
			mu.Lock()
			defer mu.Unlock()
			return sessions[t]
		}
		u, p, ok := r.BasicAuth()
		return ok && u == user && p == pass
	}
	errBody := func(w http.ResponseWriter, code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "Base.1.0.GeneralError", "message": msg, "@Message.ExtendedInfo": []map[string]string{{"Message": msg}}}})
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		if path == "" {
			path = "/"
		}
		logf("%s %s token=%v basic=%v", r.Method, path, r.Header.Get("X-Auth-Token") != "", r.Header.Get("Authorization") != "")
		if path == "/redfish/v1/SessionService/Sessions" && r.Method == http.MethodPost {
			if noSession {
				errBody(w, http.StatusNotFound, "SessionService disabled")
				return
			}
			var body struct{ UserName, Password string }
			json.NewDecoder(r.Body).Decode(&body)
			if body.UserName != user || body.Password != pass {
				errBody(w, http.StatusUnauthorized, "bad credentials")
				return
			}
			b := make([]byte, 16)
			rand.Read(b)
			tok := hex.EncodeToString(b)
			mu.Lock()
			sessions[tok] = true
			mu.Unlock()
			w.Header().Set("X-Auth-Token", tok)
			w.Header().Set("Location", "/redfish/v1/SessionService/Sessions/"+tok[:8])
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"@odata.id":"/redfish/v1/SessionService/Sessions/%s","Id":"%s","UserName":"%s"}`, tok[:8], tok[:8], user)
			return
		}
		if strings.HasPrefix(path, "/redfish/v1/SessionService/Sessions/") && r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if path == "/redfish" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"v1":"/redfish/v1/"}`)
			return
		}
		if path != "/redfish/v1" && !authed(r) {
			errBody(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if r.Method == http.MethodPost && path == "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				errBody(w, http.StatusBadRequest, "malformed JSON")
				return
			}
			rt, _ := body["ResetType"].(string)
			mu.Lock()
			var sys map[string]any
			json.Unmarshal([]byte(state["/redfish/v1/Systems/1"]), &sys)
			switch rt {
			case "On", "GracefulRestart", "ForceRestart":
				sys["PowerState"] = "On"
			case "ForceOff", "GracefulShutdown":
				sys["PowerState"] = "Off"
			default:
				mu.Unlock()
				errBody(w, http.StatusBadRequest, "ResetType "+rt+" is not supported")
				return
			}
			b, _ := json.Marshal(sys)
			state["/redfish/v1/Systems/1"] = string(b)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodPost && path == "/redfish/v1/Systems/1/Actions/Oem/Acme.Beep" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				errBody(w, http.StatusBadRequest, "malformed JSON")
				return
			}
			if _, ok := body["Times"].(float64); !ok {
				errBody(w, http.StatusBadRequest, "Times must be a number")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mu.Lock()
		body, ok := state[path]
		mu.Unlock()
		if !ok {
			errBody(w, http.StatusNotFound, "resource "+path+" not found")
			return
		}
		if r.Method == http.MethodPatch {
			var patch, cur map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				errBody(w, http.StatusBadRequest, "malformed JSON")
				return
			}
			json.Unmarshal([]byte(body), &cur)
			for k := range patch {
				if k == "Id" || k == "Name" || strings.HasPrefix(k, "@") {
					errBody(w, http.StatusBadRequest, "property "+k+" is read-only")
					return
				}
			}
			merge(cur, patch)
			b, _ := json.Marshal(cur)
			mu.Lock()
			state[path] = string(b)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"@Message.ExtendedInfo":[{"MessageId":"Base.1.0.Success","Message":"Successfully Completed Request","Severity":"OK"}]}`)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			errBody(w, http.StatusMethodNotAllowed, r.Method+" not allowed on "+path)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `W/"mock-etag"`)
		w.Header().Set("OData-Version", "4.0")
		fmt.Fprint(w, body)
	})
	return mux
}

// merge applies a JSON merge-patch style update of src onto dst.
func merge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				merge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}
