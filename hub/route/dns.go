package route

import (
	"context"
	"math"
	"net/http"
	"net/netip"

	"github.com/metacubex/mihomo/component/resolver"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/miekg/dns"
	"github.com/samber/lo"
)

func dnsRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/query", queryDNS)
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

	// 首先尝试从 hosts 中查找
	var ips []netip.Addr
	var err error

	switch qType {
	case dns.TypeA:
		ips, err = resolver.LookupIPv4WithResolver(ctx, name, resolver.DefaultResolver)
	case dns.TypeAAAA:
		ips, err = resolver.LookupIPv6WithResolver(ctx, name, resolver.DefaultResolver)
	default:
		// 对于其他类型的查询，仍然使用原始的 DNS 查询
		msg := dns.Msg{}
		msg.SetQuestion(dns.Fqdn(name), qType)
		resp, err := resolver.DefaultResolver.ExchangeContext(ctx, &msg)
		if err != nil {
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, newError(err.Error()))
			return
		}
		renderDNSResponse(w, r, resp)
		return
	}

	if err != nil {
		render.Status(r, http.StatusInternalServerError)
		render.JSON(w, r, newError(err.Error()))
		return
	}

	// 构造 DNS 响应格式
	question := dns.Question{
		Name:   dns.Fqdn(name),
		Qtype:  qType,
		Qclass: dns.ClassINET,
	}

	responseData := render.M{
		"Status":   dns.RcodeSuccess,
		"Question": []dns.Question{question},
		"TC":       false,
		"RD":       true,
		"RA":       true,
		"AD":       false,
		"CD":       false,
	}

	// 构造 Answer 记录
	var answers []render.M
	for _, ip := range ips {
		answers = append(answers, render.M{
			"name": dns.Fqdn(name),
			"type": qType,
			"TTL":  300, // 默认 TTL
			"data": ip.String(),
		})
	}

	if len(answers) > 0 {
		responseData["Answer"] = answers
	}

	render.JSON(w, r, responseData)
}

// 渲染原始 DNS 响应（用于非 A/AAAA 记录）
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
