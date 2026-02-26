package route

import (
	"encoding/json"
	"net/http"

	"github.com/metacubex/mihomo/component/netsim"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

func netSimRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", getNetSim)
	r.Put("/", updateNetSim)
	r.Patch("/", patchNetSim)
	r.Delete("/", deleteNetSim)
	r.Get("/stats", getNetSimStats)
	r.Delete("/stats", resetNetSimStats)
	return r
}

func getNetSim(w http.ResponseWriter, r *http.Request) {
	cfg := netsim.GetConfig()
	render.JSON(w, r, cfg)
}

func updateNetSim(w http.ResponseWriter, r *http.Request) {
	var cfg netsim.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, render.M{"error": err.Error()})
		return
	}
	if cfg.QueueType != "" && !netsim.ValidQueueType(cfg.QueueType) {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, render.M{"error": "invalid queue-type, must be: fifo, prio, or fair-queue"})
		return
	}
	if cfg.QueueType == "" {
		cfg.QueueType = netsim.QueueFIFO
	}
	netsim.UpdateConfig(&cfg)
	render.JSON(w, r, render.M{"message": "net-sim updated", "config": cfg})
}

func patchNetSim(w http.ResponseWriter, r *http.Request) {
	current := netsim.GetConfig()
	raw := make(map[string]json.RawMessage)
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, render.M{"error": err.Error()})
		return
	}

	currentBytes, _ := json.Marshal(current)
	var merged map[string]json.RawMessage
	_ = json.Unmarshal(currentBytes, &merged)
	for k, v := range raw {
		merged[k] = v
	}
	mergedBytes, _ := json.Marshal(merged)

	var cfg netsim.Config
	if err := json.Unmarshal(mergedBytes, &cfg); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, render.M{"error": err.Error()})
		return
	}
	if cfg.QueueType != "" && !netsim.ValidQueueType(cfg.QueueType) {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, render.M{"error": "invalid queue-type"})
		return
	}
	netsim.UpdateConfig(&cfg)
	render.JSON(w, r, render.M{"message": "net-sim patched", "config": cfg})
}

func deleteNetSim(w http.ResponseWriter, r *http.Request) {
	netsim.UpdateConfig(netsim.DefaultConfig())
	render.JSON(w, r, render.M{"message": "net-sim disabled and reset"})
}

func getNetSimStats(w http.ResponseWriter, r *http.Request) {
	render.JSON(w, r, netsim.GetStats())
}

func resetNetSimStats(w http.ResponseWriter, r *http.Request) {
	netsim.ResetStats()
	render.JSON(w, r, render.M{"message": "stats reset"})
}
