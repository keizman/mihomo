package route

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/metacubex/mihomo/component/resolver"
	mdns "github.com/metacubex/mihomo/dns"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/miekg/dns"
	"github.com/samber/lo"
)

func dnsRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/query", queryDNS)
	r.Post("/delay", setDnsDelay)
	r.Get("/delay", getDnsDelay)
	return r
}

func queryDNS(w http.ResponseWriter, r *http.Request) {
	if resolver.DefaultResolver == nil {
		render.Status(r, http.StatusInternalServerError)
		render.JSON(w, r, newError("DNS section is disabled"))
		return
	}

	name := r.URL.Query().Get("name")
	qTypeStr, _ := lo.Coalesce(r.URL.Query().Get("type"), "A")

	qType, exist := dns.StringToType[qTypeStr]
	if !exist {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("invalid query type"))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), resolver.DefaultDNSTimeout)
	defer cancel()

	// 使用原始 DNS 查询获取完整响应信息（包括真实 TTL）
	msg := dns.Msg{}
	msg.SetQuestion(dns.Fqdn(name), qType)
	resp, err := resolver.DefaultResolver.ExchangeContext(ctx, &msg)
	if err != nil {
		render.Status(r, http.StatusInternalServerError)
		render.JSON(w, r, newError(err.Error()))
		return
	}
	
	renderDNSResponse(w, r, resp)
}

// 渲染原始 DNS 响应（支持所有记录类型，包括 A/AAAA/CNAME/MX 等）
func renderDNSResponse(w http.ResponseWriter, r *http.Request, resp *dns.Msg) {
	responseData := render.M{
		"Status":   resp.Rcode,
		"Question": resp.Question,
		"TC":       resp.Truncated,
		"RD":       resp.RecursionDesired,
		"RA":       resp.RecursionAvailable,
		"AD":       resp.AuthenticatedData,
		"CD":       resp.CheckingDisabled,
	}

	rr2Json := func(rr dns.RR, _ int) render.M {
		header := rr.Header()
		return render.M{
			"name": header.Name,
			"type": header.Rrtype,
			"TTL":  header.Ttl,
			"data": lo.Substring(rr.String(), len(header.String()), math.MaxUint),
		}
	}

	if len(resp.Answer) > 0 {
		responseData["Answer"] = lo.Map(resp.Answer, rr2Json)
	}
	if len(resp.Ns) > 0 {
		responseData["Authority"] = lo.Map(resp.Ns, rr2Json)
	}
	if len(resp.Extra) > 0 {
		responseData["Additional"] = lo.Map(resp.Extra, rr2Json)
	}

	render.JSON(w, r, responseData)
}

func setDnsDelay(w http.ResponseWriter, r *http.Request) {
	delayMs := r.URL.Query().Get("delay")
	if delayMs == "" {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("missing delay parameter (in milliseconds)"))
		return
	}

	delay, err := strconv.Atoi(delayMs)
	if err != nil || delay < 0 {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("invalid delay value, must be a non-negative integer"))
		return
	}

	mdns.SetDNSDelay(time.Duration(delay) * time.Millisecond)
	render.NoContent(w, r)
}

func getDnsDelay(w http.ResponseWriter, r *http.Request) {
	delay := mdns.GetDNSDelay()
	render.JSON(w, r, map[string]interface{}{
		"delay_ms": delay.Milliseconds(),
	})
}
