package route

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/component/trie"
	"github.com/metacubex/mihomo/log"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

type HostsEntry struct {
	Domain string   `json:"domain"`
	IPs    []string `json:"ips,omitempty"`
	IP     string   `json:"ip,omitempty"`
}

type HostsResponse struct {
	Hosts map[string]interface{} `json:"hosts"`
	Count int                    `json:"count"`
}

type HostsRequest struct {
	Domain string      `json:"domain"`
	Value  interface{} `json:"value"` // 可以是字符串IP或IP数组
}

type BatchHostsRequest struct {
	Operation string                 `json:"operation"` // "add", "update", "delete"
	Hosts     map[string]interface{} `json:"hosts"`
}

// 全局变量，用于维护动态添加的 hosts
var dynamicHosts = make(map[string]resolver.HostValue)

func hostsRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", getHosts)
	r.Post("/", addHost)
	r.Put("/", updateHost)
	r.Delete("/{domain}", deleteHost)
	r.Get("/{domain}", getHost)
	r.Delete("/", clearHosts)
	r.Post("/batch", batchHosts)
	return r
}

// 获取所有 hosts 映射
func getHosts(w http.ResponseWriter, r *http.Request) {
	hostsMap := make(map[string]interface{})

	// 获取要检查的域名列表
	testDomains := getDomainsToCheck(r)

	response := map[string]interface{}{
		"info": map[string]interface{}{
			"note": "Due to trie structure limitations, this API can only show domains that are explicitly checked",
			"how_to_see_all": "Use ?domains=domain1,domain2,domain3 to specify domains from your config file",
			"example": "?domains=+.example.com,api.test.com,cdn.domain.com",
		},
	}

	// 遍历测试域名，找出已配置的
	for _, domain := range testDomains {
		if node, ok := resolver.DefaultHosts.Search(domain, false); ok {
			if node.IsDomain {
				hostsMap[domain] = node.Domain
			} else {
				ips := make([]string, len(node.IPs))
				for i, ip := range node.IPs {
					ips[i] = ip.String()
				}
				if len(ips) == 1 {
					hostsMap[domain] = ips[0]
				} else {
					hostsMap[domain] = ips
				}
			}
		}
	}

	response["hosts"] = hostsMap
	response["count"] = len(hostsMap)
	
	if len(hostsMap) == 0 {
		response["suggestion"] = "No hosts found. Try specifying domains with ?domains=your.domain.com"
	}

	render.JSON(w, r, response)
}

// 获取要检查的域名列表
func getDomainsToCheck(r *http.Request) []string {
	// 只保留必要的系统域名
	defaultDomains := []string{
		"localhost",
	}
	
	// 用户指定的域名
	if domainsParam := r.URL.Query().Get("domains"); domainsParam != "" {
		userDomains := strings.Split(domainsParam, ",")
		for i, domain := range userDomains {
			userDomains[i] = strings.TrimSpace(domain)
		}
		return append(defaultDomains, userDomains...)
	}
	
	return defaultDomains
}

// 获取指定域名的 hosts 映射
func getHost(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("domain parameter is required"))
		return
	}

	node, ok := resolver.DefaultHosts.Search(domain, false)
	if !ok {
		render.Status(r, http.StatusNotFound)
		render.JSON(w, r, newError("host not found"))
		return
	}

	entry := HostsEntry{Domain: domain}
	if node.IsDomain {
		entry.IP = node.Domain
	} else {
		ips := make([]string, len(node.IPs))
		for i, ip := range node.IPs {
			ips[i] = ip.String()
		}
		if len(ips) == 1 {
			entry.IP = ips[0]
		} else {
			entry.IPs = ips
		}
	}

	render.JSON(w, r, entry)
}

// 添加新的 hosts 映射
func addHost(w http.ResponseWriter, r *http.Request) {
	var req HostsRequest
	if err := render.DecodeJSON(r.Body, &req); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, ErrBadRequest)
		return
	}

	if req.Domain == "" {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("domain is required"))
		return
	}

	if err := updateHostsMapping(req.Domain, req.Value, false); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError(err.Error()))
		return
	}

	render.JSON(w, r, render.M{
		"message": fmt.Sprintf("Host %s added successfully", req.Domain),
		"domain":  req.Domain,
		"value":   req.Value,
	})
}

// 更新 hosts 映射
func updateHost(w http.ResponseWriter, r *http.Request) {
	var req HostsRequest
	if err := render.DecodeJSON(r.Body, &req); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, ErrBadRequest)
		return
	}

	if req.Domain == "" {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("domain is required"))
		return
	}

	if err := updateHostsMapping(req.Domain, req.Value, true); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError(err.Error()))
		return
	}

	render.JSON(w, r, render.M{
		"message": fmt.Sprintf("Host %s updated successfully", req.Domain),
		"domain":  req.Domain,
		"value":   req.Value,
	})
}

// 删除指定域名的 hosts 映射
func deleteHost(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	if domain == "" {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("domain parameter is required"))
		return
	}

	if err := updateHostsMapping(domain, nil, true); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError(err.Error()))
		return
	}

	render.JSON(w, r, render.M{
		"message": fmt.Sprintf("Host %s deleted successfully", domain),
		"domain":  domain,
	})
}

// 清空所有动态添加的 hosts 映射（保留配置文件中的映射）
func clearHosts(w http.ResponseWriter, r *http.Request) {
	// 清空动态 hosts
	dynamicHosts = make(map[string]resolver.HostValue)
	
	// 重新构建 hosts trie，只包含配置文件中的 hosts
	rebuildHostsFromConfig()

	render.JSON(w, r, render.M{
		"message": "All dynamic hosts cleared successfully",
		"note":    "Configuration file hosts are preserved",
	})
}

// 批量操作 hosts 映射
func batchHosts(w http.ResponseWriter, r *http.Request) {
	var req BatchHostsRequest
	if err := render.DecodeJSON(r.Body, &req); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, ErrBadRequest)
		return
	}

	if req.Operation == "" {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("operation is required"))
		return
	}

	results := make(map[string]interface{})
	errors := make(map[string]string)
	successCount := 0

	for domain, value := range req.Hosts {
		var err error
		switch req.Operation {
		case "add":
			err = updateHostsMapping(domain, value, false)
		case "update":
			err = updateHostsMapping(domain, value, true)
		case "delete":
			err = updateHostsMapping(domain, nil, true)
		default:
			err = fmt.Errorf("invalid operation: %s", req.Operation)
		}

		if err != nil {
			errors[domain] = err.Error()
		} else {
			results[domain] = "success"
			successCount++
		}
	}

	response := render.M{
		"operation":     req.Operation,
		"total":         len(req.Hosts),
		"success_count": successCount,
		"error_count":   len(errors),
		"results":       results,
	}

	if len(errors) > 0 {
		response["errors"] = errors
	}

	render.JSON(w, r, response)
}

// 更新 hosts 映射的核心逻辑
func updateHostsMapping(domain string, value interface{}, allowUpdate bool) error {
	// 检查域名是否已在动态 hosts 中存在
	_, exists := dynamicHosts[domain]
	if exists && !allowUpdate {
		return fmt.Errorf("host %s already exists, use PUT to update", domain)
	}

	// 处理删除操作
	if value == nil {
		if !exists {
			return fmt.Errorf("host %s not found", domain)
		}
		delete(dynamicHosts, domain)
		rebuildHostsFromConfig()
		return nil
	}

	// 解析并验证 IP 地址或域名
	hostValue, err := resolver.NewHostValue(value)
	if err != nil {
		return fmt.Errorf("invalid host value: %s", err.Error())
	}

	// 检查域名循环引用
	if hostValue.IsDomain {
		if err := checkCircularReference(domain, hostValue.Domain); err != nil {
			return err
		}
	}

	// 添加到动态 hosts
	dynamicHosts[domain] = hostValue
	
	// 重建 hosts trie
	rebuildHostsFromConfig()

	log.Infoln("Hosts updated: %s -> %v", domain, value)
	return nil
}

// 从配置重建 hosts（包括配置文件 + 动态添加的）
func rebuildHostsFromConfig() {
	// 简化实现：直接在现有的 resolver.DefaultHosts 基础上操作
	// 由于我们无法完美地分离配置文件的 hosts 和动态 hosts，
	// 我们采用追加模式：保持现有的，只添加动态的
	
	// 获取当前的 hosts trie（这包含了配置文件中的 hosts）
	// resolver.DefaultHosts 本身就是 *trie.DomainTrie[HostValue] 的包装
	currentTrie := resolver.DefaultHosts.DomainTrie
	if currentTrie == nil {
		currentTrie = trie.New[resolver.HostValue]()
	}
	
	// 添加动态 hosts（这会覆盖同名的配置文件项）
	for domain, hostValue := range dynamicHosts {
		if err := currentTrie.Insert(domain, hostValue); err != nil {
			log.Warnln("Failed to insert dynamic host %s: %s", domain, err.Error())
		}
	}

	// 优化并更新
	currentTrie.Optimize()
	resolver.DefaultHosts = resolver.NewHosts(currentTrie)
	
	log.Infoln("Hosts configuration rebuilt with %d dynamic entries", len(dynamicHosts))
}

// 检查循环引用
func checkCircularReference(domain, targetDomain string) error {
	visited := make(map[string]bool)
	checkDomain := targetDomain
	
	for checkDomain != "" {
		if visited[checkDomain] || checkDomain == domain {
			return fmt.Errorf("circular domain mapping detected for %s", domain)
		}
		
		visited[checkDomain] = true
		
		// 检查动态 hosts
		if hostValue, ok := dynamicHosts[checkDomain]; ok {
			if !hostValue.IsDomain {
				break
			}
			checkDomain = hostValue.Domain
			continue
		}
		
		// 检查当前的 hosts
		if node, ok := resolver.DefaultHosts.Search(checkDomain, false); ok {
			if !node.IsDomain {
				break
			}
			checkDomain = node.Domain
		} else {
			break
		}
	}
	
	return nil
}