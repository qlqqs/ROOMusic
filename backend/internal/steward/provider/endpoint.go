package provider

import (
	"net"
	"net/url"
	"strings"
)

type preset struct {
	name     string
	host     string
	base     string
	protocol Protocol
}

// 只登记 provider-protocols.md 第 4 节核实的地址；它们只提供猜测，不证明工具能力。
// 同主机的更具体路径在前；本地端口允许 localhost 的回环 IP 写法。
var presets = []preset{
	{"DeepSeek", "api.deepseek.com", "/anthropic", ProtocolMessages},
	{"DeepSeek", "api.deepseek.com", "", ProtocolOpenAI},
	{"OpenAI", "api.openai.com", "/v1", ProtocolOpenAI},
	{"Anthropic", "api.anthropic.com", "/v1", ProtocolMessages},
	{"Ollama", "localhost:11434", "/v1", ProtocolOpenAI},
	{"Ollama", "ollama.com", "/v1", ProtocolOpenAI},
	{"LM Studio", "localhost:1234", "/v1", ProtocolOpenAI},
	{"vLLM", "localhost:8000", "/v1", ProtocolOpenAI},
	{"OpenRouter", "openrouter.ai", "/api/v1", ProtocolOpenAI},
	{"Google Gemini", "generativelanguage.googleapis.com", "/v1beta/openai", ProtocolOpenAI},
	{"Groq", "api.groq.com", "/openai/v1", ProtocolOpenAI},
	{"Mistral", "api.mistral.ai", "/v1", ProtocolOpenAI},
	{"xAI", "api.x.ai", "/v1", ProtocolOpenAI},
	{"Moonshot", "api.moonshot.cn", "/v1", ProtocolOpenAI},
	{"硅基流动", "api.siliconflow.cn", "/v1", ProtocolOpenAI},
	{"智谱", "open.bigmodel.cn", "/api/paas/v4", ProtocolOpenAI},
}

func matchPreset(u *url.URL) *preset {
	host := strings.ToLower(u.Host)
	if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
		host = "localhost"
		if u.Port() != "" {
			host += ":" + u.Port()
		}
	}
	path := strings.TrimRight(u.Path, "/")
	for i := range presets {
		p := &presets[i]
		if host != p.host {
			continue
		}
		if p.base == "/anthropic" && path != p.base && !strings.HasPrefix(path, p.base+"/") {
			continue
		}
		return p
	}
	return nil
}

func parseEndpoint(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, failure(ErrorOther, 0, "端点必须是无凭据、查询参数和片段的 HTTP(S) 基础地址", "")
	}
	return u, nil
}

func endpointURL(endpoint string, protocol Protocol, resource string) (string, error) {
	u, err := parseEndpoint(endpoint)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(u.Path, "/")
	// 已带版本或代理前缀的地址保留原路径；裸地址默认补 /v1。
	// 已知服务使用表中的默认前缀：DeepSeek 没有 /v1，智谱为 /api/paas/v4，
	// Gemini 为 /v1beta/openai，Groq 为 /openai/v1，OpenRouter 为 /api/v1。
	// Messages 在 /v1 后拼 /messages；DeepSeek 的 /anthropic 是代理基础地址，
	// 按 Messages 约定在其后拼 /v1/messages（模型列表同理）。
	if path == "" {
		path = "/v1"
		if p := matchPreset(u); p != nil && (p.protocol == protocol || (protocol == ProtocolResponses && p.protocol == ProtocolOpenAI)) {
			path = p.base
		}
	}
	if protocol == ProtocolMessages && strings.HasSuffix(path, "/anthropic") {
		path += "/v1"
	}
	u.Path = path + "/" + resource
	u.RawPath = ""
	return u.String(), nil
}
