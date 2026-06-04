package builder

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MavenServiceSuggestion 是从 Maven 项目结构中推断出的可部署 service。
//
// 它只做“建议”，不直接代表一定可部署；调用方可以用 Recommended/Confidence
// 决定默认勾选哪些项，再让用户确认端口和启动顺序。
type MavenServiceSuggestion struct {
	ServiceCode     string `json:"service_code"`
	Name            string `json:"name"`
	BuildModule     string `json:"build_module"`
	BuildJarPattern string `json:"build_jar_pattern"`
	Port            int    `json:"port"`
	HealthCheckURL  string `json:"health_check_url"`
	StartupOrder    int    `json:"startup_order"`
	Recommended     bool   `json:"recommended"`
	Confidence      string `json:"confidence"` // high / medium / low
	Reason          string `json:"reason"`
	ArtifactID      string `json:"artifact_id"`
	Packaging       string `json:"packaging"`
}

type mavenPOM struct {
	XMLName    xml.Name `xml:"project"`
	GroupID    string   `xml:"groupId"`
	ArtifactID string   `xml:"artifactId"`
	Packaging  string   `xml:"packaging"`
	Modules    []string `xml:"modules>module"`
	Parent     struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
		Version    string `xml:"version"`
	} `xml:"parent"`
}

// DiscoverMavenServices 扫描 root/pom.xml 及其 Maven modules，推断 Spring Boot
// service 清单。它不会执行 mvn，也不会写任何文件。
func DiscoverMavenServices(root string, defaultPort int) ([]MavenServiceSuggestion, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("root is empty")
	}
	if defaultPort <= 0 || defaultPort > 65535 {
		defaultPort = 8080
	}
	if _, err := os.Stat(filepath.Join(root, "pom.xml")); err != nil {
		return nil, fmt.Errorf("pom.xml not found in %s: %w", root, err)
	}
	var out []MavenServiceSuggestion
	seenPom := map[string]bool{}
	usedCodes := map[string]int{}
	if err := discoverMavenModule(root, ".", defaultPort, seenPom, usedCodes, &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartupOrder != out[j].StartupOrder {
			return out[i].StartupOrder < out[j].StartupOrder
		}
		return out[i].ServiceCode < out[j].ServiceCode
	})
	return out, nil
}

func discoverMavenModule(root, rel string, defaultPort int, seenPom map[string]bool, usedCodes map[string]int, out *[]MavenServiceSuggestion) error {
	rel = filepath.Clean(rel)
	if rel == "" {
		rel = "."
	}
	pomPath := filepath.Join(root, rel, "pom.xml")
	absPom, err := filepath.Abs(pomPath)
	if err == nil {
		if seenPom[absPom] {
			return nil
		}
		seenPom[absPom] = true
	}
	data, err := os.ReadFile(pomPath)
	if err != nil {
		return nil // module pom 缺失时跳过，让扫描尽量宽容
	}
	var pom mavenPOM
	if err := xml.Unmarshal(data, &pom); err != nil {
		return fmt.Errorf("parse %s: %w", pomPath, err)
	}

	for _, m := range pom.Modules {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		child := filepath.Clean(filepath.Join(rel, filepath.FromSlash(m)))
		_ = discoverMavenModule(root, child, defaultPort, seenPom, usedCodes, out)
	}

	packaging := strings.TrimSpace(pom.Packaging)
	if packaging == "" {
		packaging = "jar"
	}
	if packaging == "pom" {
		return nil
	}

	moduleDir := filepath.Join(root, rel)
	port, portReason := detectSpringPort(moduleDir)
	bootEntry := hasSpringBootEntry(moduleDir)
	bootPlugin := hasSpringBootPlugin(string(data))
	starterSignal := hasSpringStarter(string(data))
	serviceLike := looksLikeDeployableModule(rel, pom.ArtifactID)
	libraryLike := looksLikeLibraryModule(rel, pom.ArtifactID)
	if libraryLike && !bootEntry && !bootPlugin && port == 0 {
		return nil
	}
	if !bootEntry && !bootPlugin && port == 0 && !serviceLike {
		return nil
	}

	codeBase := strings.TrimSpace(pom.ArtifactID)
	if codeBase == "" {
		codeBase = filepath.Base(rel)
	}
	code := uniqueServiceCode(normalizeServiceCode(codeBase), rel, usedCodes)
	if code == "" {
		return nil
	}

	confidence := "low"
	recommended := false
	reasons := []string{}
	if bootEntry || bootPlugin {
		confidence = "high"
		recommended = true
		if bootEntry {
			reasons = append(reasons, "检测到 Spring Boot 启动类")
		} else {
			reasons = append(reasons, "检测到 spring-boot-maven-plugin")
		}
	} else if port > 0 {
		confidence = "medium"
		recommended = true
		reasons = append(reasons, "检测到 server.port")
	} else if starterSignal && serviceLike {
		confidence = "medium"
		recommended = true
		reasons = append(reasons, "检测到 Spring Boot 依赖且模块名像服务")
	} else {
		reasons = append(reasons, "模块名像可部署服务，但未检测到 Spring Boot 明确信号")
	}
	if port > 0 {
		reasons = append(reasons, portReason)
	} else {
		port = defaultPort
		reasons = append(reasons, fmt.Sprintf("未识别端口，暂用应用默认端口 %d，请确认", defaultPort))
	}

	modulePath := ""
	if rel != "." {
		modulePath = filepath.ToSlash(rel)
	}
	jarPattern := "target/*.jar"
	if modulePath != "" {
		jarPattern = modulePath + "/target/*.jar"
	}
	*out = append(*out, MavenServiceSuggestion{
		ServiceCode:     code,
		Name:            code,
		BuildModule:     modulePath,
		BuildJarPattern: jarPattern,
		Port:            port,
		HealthCheckURL:  "/actuator/health",
		StartupOrder:    inferStartupOrder(code),
		Recommended:     recommended,
		Confidence:      confidence,
		Reason:          strings.Join(reasons, "；"),
		ArtifactID:      strings.TrimSpace(pom.ArtifactID),
		Packaging:       packaging,
	})
	return nil
}

func normalizeServiceCode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return ""
	}
	if out[0] < 'a' || out[0] > 'z' {
		out = "svc-" + out
	}
	if len(out) > 50 {
		out = strings.TrimRight(out[:50], "-")
	}
	if len(out) < 2 {
		out += "-svc"
	}
	return out
}

func uniqueServiceCode(code, rel string, used map[string]int) string {
	if code == "" {
		code = normalizeServiceCode(filepath.Base(rel))
	}
	if code == "" {
		return ""
	}
	if used[code] == 0 {
		used[code] = 1
		return code
	}
	used[code]++
	suffix := fmt.Sprintf("-%d", used[code])
	maxBase := 50 - len(suffix)
	if maxBase < 2 {
		return ""
	}
	base := strings.TrimRight(code[:minInt(len(code), maxBase)], "-")
	out := base + suffix
	used[out] = 1
	return out
}

func hasSpringBootPlugin(pomText string) bool {
	lowerPom := strings.ToLower(pomText)
	return strings.Contains(lowerPom, "spring-boot-maven-plugin")
}

func hasSpringStarter(pomText string) bool {
	lowerPom := strings.ToLower(pomText)
	return strings.Contains(lowerPom, "spring-boot-starter") ||
		strings.Contains(lowerPom, "spring-cloud-starter")
}

func hasSpringBootEntry(moduleDir string) bool {
	src := filepath.Join(moduleDir, "src", "main", "java")
	found := false
	_ = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || found {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".java") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		if strings.Contains(text, "@SpringBootApplication") ||
			strings.Contains(text, "@EnableDiscoveryClient") ||
			strings.Contains(text, "@EnableEurekaClient") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func looksLikeDeployableModule(rel, artifactID string) bool {
	name := strings.ToLower(strings.TrimSpace(artifactID))
	if name == "" {
		name = strings.ToLower(filepath.Base(rel))
	}
	serviceHints := []string{"service", "server", "gateway", "admin", "auth", "job", "worker", "consumer", "provider", "api-server"}
	for _, h := range serviceHints {
		if strings.Contains(name, h) {
			return true
		}
	}
	libraryHints := []string{"common", "core", "model", "entity", "dto", "dao", "mapper", "sdk", "client", "starter"}
	for _, h := range libraryHints {
		if strings.Contains(name, h) {
			return false
		}
	}
	return false
}

func looksLikeLibraryModule(rel, artifactID string) bool {
	name := strings.ToLower(strings.TrimSpace(artifactID))
	if name == "" {
		name = strings.ToLower(filepath.Base(rel))
	}
	libraryHints := []string{"common", "core", "model", "entity", "dto", "dao", "mapper", "sdk", "client", "domain", "facade", "contract", "starter"}
	for _, h := range libraryHints {
		if strings.Contains(name, h) {
			return true
		}
	}
	return false
}

var serverPortLineRE = regexp.MustCompile(`(?m)^\s*server\.port\s*[:=]\s*"?\$\{[^:}]+:([0-9]{1,5})\}"?\s*$|(?m)^\s*server\.port\s*[:=]\s*"?([0-9]{1,5})"?\s*$`)

func detectSpringPort(moduleDir string) (int, string) {
	resDir := filepath.Join(moduleDir, "src", "main", "resources")
	files, _ := filepath.Glob(filepath.Join(resDir, "application*.properties"))
	ymls, _ := filepath.Glob(filepath.Join(resDir, "application*.yml"))
	yamls, _ := filepath.Glob(filepath.Join(resDir, "application*.yaml"))
	boots, _ := filepath.Glob(filepath.Join(resDir, "bootstrap*.properties"))
	bootYmls, _ := filepath.Glob(filepath.Join(resDir, "bootstrap*.yml"))
	bootYamls, _ := filepath.Glob(filepath.Join(resDir, "bootstrap*.yaml"))
	files = append(files, ymls...)
	files = append(files, yamls...)
	files = append(files, boots...)
	files = append(files, bootYmls...)
	files = append(files, bootYamls...)
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if p := parsePortFromText(string(data)); p > 0 {
			rel, _ := filepath.Rel(moduleDir, f)
			return p, fmt.Sprintf("从 %s 读取 server.port=%d", filepath.ToSlash(rel), p)
		}
	}
	return 0, ""
}

func parsePortFromText(text string) int {
	if matches := serverPortLineRE.FindStringSubmatch(text); len(matches) > 0 {
		for _, m := range matches[1:] {
			if p := parsePort(m); p > 0 {
				return p
			}
		}
	}
	lines := strings.Split(text, "\n")
	serverIndent := -1
	for _, line := range lines {
		raw := strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if strings.HasPrefix(trimmed, "server:") {
			serverIndent = indent
			continue
		}
		if serverIndent >= 0 {
			if indent <= serverIndent {
				serverIndent = -1
				continue
			}
			if strings.HasPrefix(trimmed, "port:") {
				val := strings.TrimSpace(strings.TrimPrefix(trimmed, "port:"))
				val = strings.Trim(val, `"'`)
				if p := parsePortValue(val); p > 0 {
					return p
				}
			}
		}
	}
	return 0
}

func parsePortValue(s string) int {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "${") && strings.Contains(s, ":") && strings.HasSuffix(s, "}") {
		s = strings.TrimSuffix(s[strings.LastIndex(s, ":")+1:], "}")
	}
	return parsePort(strings.Trim(s, `"'`))
}

func parsePort(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 || n > 65535 {
		return 0
	}
	return n
}

func inferStartupOrder(code string) int {
	c := strings.ToLower(code)
	switch {
	case strings.Contains(c, "nacos"),
		strings.Contains(c, "eureka"),
		strings.Contains(c, "registry"),
		strings.Contains(c, "discovery"),
		strings.Contains(c, "config"):
		return 0
	case strings.Contains(c, "gateway"):
		return 10
	default:
		return 100
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
