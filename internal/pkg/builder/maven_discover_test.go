package builder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverMavenServices_MultiModuleSpringBoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId>
  <artifactId>demo-parent</artifactId>
  <packaging>pom</packaging>
  <modules>
    <module>demo-auth-server</module>
    <module>demo-gateway</module>
    <module>demo-common</module>
  </modules>
</project>`)
	writeFile(t, filepath.Join(root, "demo-auth-server", "pom.xml"), `
<project>
  <artifactId>demo-auth-server</artifactId>
  <dependencies><dependency><artifactId>spring-boot-starter-web</artifactId></dependency></dependencies>
</project>`)
	writeFile(t, filepath.Join(root, "demo-auth-server", "src", "main", "resources", "application.yml"), `
server:
  port: 8083
`)
	writeFile(t, filepath.Join(root, "demo-gateway", "pom.xml"), `
<project><artifactId>demo-gateway</artifactId><packaging>jar</packaging></project>`)
	writeFile(t, filepath.Join(root, "demo-gateway", "src", "main", "java", "demo", "GatewayApplication.java"), `
@SpringBootApplication
class GatewayApplication {}
`)
	writeFile(t, filepath.Join(root, "demo-gateway", "src", "main", "resources", "application.properties"), `
server.port=${SERVER_PORT:9000}
`)
	writeFile(t, filepath.Join(root, "demo-common", "pom.xml"), `
<project><artifactId>demo-common</artifactId></project>`)

	got, err := DiscoverMavenServices(root, 8080)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2: %#v", len(got), got)
	}
	byCode := map[string]MavenServiceSuggestion{}
	for _, s := range got {
		byCode[s.ServiceCode] = s
	}
	auth := byCode["demo-auth-server"]
	if auth.BuildModule != "demo-auth-server" || auth.BuildJarPattern != "demo-auth-server/target/*.jar" || auth.Port != 8083 {
		t.Fatalf("auth suggestion unexpected: %#v", auth)
	}
	gw := byCode["demo-gateway"]
	if gw.StartupOrder != 10 || gw.Port != 9000 || !gw.Recommended {
		t.Fatalf("gateway suggestion unexpected: %#v", gw)
	}
	if _, ok := byCode["demo-common"]; ok {
		t.Fatal("common module should not be suggested")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
