package cli

import (
	"strings"
	"testing"
)

func TestDeployFilesPerStack(t *testing.T) {
	for _, fw := range []string{"laravel", "django", "fastapi", "nextjs"} {
		rt, ok := runtimeFor(fw)
		if !ok || rt.Port == "" || rt.Dockerfile == "" {
			t.Fatalf("%s: missing runtime", fw)
		}
		for _, target := range deployTargets {
			files, err := deployFiles(fw, target)
			if err != nil {
				t.Fatalf("%s/%s: %v", fw, target, err)
			}
			if files["DEPLOY.md"] == "" {
				t.Errorf("%s/%s: expected DEPLOY.md", fw, target)
			}
			// Vercel is build-config (no Dockerfile); every other target ships one.
			if target != "vercel" && files["Dockerfile"] == "" {
				t.Errorf("%s/%s: expected Dockerfile", fw, target)
			}
			switch target {
			case "vercel":
				if files["vercel.json"] == "" {
					t.Errorf("%s/vercel: missing vercel.json", fw)
				}
			case "compose":
				if files["docker-compose.prod.yml"] == "" || files["Caddyfile"] == "" {
					t.Errorf("%s/compose: missing compose or Caddyfile", fw)
				}
			case "vps":
				if files["deploy.sh"] == "" {
					t.Errorf("%s/vps: missing deploy.sh", fw)
				}
			case "fly":
				if !strings.Contains(files["fly.toml"], "internal_port = "+rt.Port) {
					t.Errorf("%s/fly: fly.toml port not wired to %s", fw, rt.Port)
				}
			case "render":
				if !strings.Contains(files["render.yaml"], "dockerfilePath") {
					t.Errorf("%s/render: render.yaml missing", fw)
				}
			case "railway":
				if !strings.Contains(files["railway.json"], "DOCKERFILE") {
					t.Errorf("%s/railway: railway.json missing", fw)
				}
			}
			// The Caddy proxy must target the app's real port.
			if c := files["Caddyfile"]; c != "" && !strings.Contains(c, "app:"+rt.Port) {
				t.Errorf("%s/%s: Caddyfile not proxying app:%s", fw, target, rt.Port)
			}
		}
	}
}

func TestDeployUnknownStackAndTarget(t *testing.T) {
	// Magento: guidance-only, never a broken artifact.
	files, err := deployFiles("magento", "compose")
	if err != nil || files["DEPLOY.md"] == "" || files["Dockerfile"] != "" {
		t.Errorf("magento should yield guidance-only DEPLOY.md, got %v (err %v)", files, err)
	}
	if _, err := deployFiles("laravel", "bogus"); err == nil {
		t.Error("expected error for unknown target")
	}
}
