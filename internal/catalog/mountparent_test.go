package catalog

import (
	"strings"
	"testing"
)

// TestNamedVolumesInsideTheBindMountHaveAPlaceholder.
//
// Docker creates any missing parent of a mount point, and it creates it as
// root. A named volume mounted at a path inside the project bind mount
// therefore materialises that path on the host owned by root: it turns up in
// `git status`, the application cannot write to it, and deleting your own
// project needs sudo.
//
// Writing a placeholder file into the directory makes keel create it as the
// invoking user first, leaving only the mount itself to the daemon.
func TestNamedVolumesInsideTheBindMountHaveAPlaceholder(t *testing.T) {
	// Where each framework's project tree is mounted in its container.
	roots := []string{"/app/", "/var/www/html/"}

	eachComposeDoc(t, func(where string, doc composeDoc, files map[string]string) {
		for svc, s := range doc.Services {
			for _, v := range s.Volumes {
				parts := strings.Split(v, ":")
				if len(parts) < 2 {
					continue
				}
				src, dst := parts[0], parts[1]
				if _, named := doc.Volumes[src]; !named {
					continue
				}
				for _, root := range roots {
					rel, inside := strings.CutPrefix(dst, root)
					if !inside || rel == "" {
						continue
					}
					guarded := false
					for p := range files {
						if strings.HasPrefix(p, rel+"/") {
							guarded = true
						}
					}
					if !guarded {
						t.Errorf("%s: service %q mounts the named volume %q at %s, inside "+
							"the bind mount, so Docker creates %s on the host as root. Write a "+
							"placeholder file inside %s so keel creates it as the user first.",
							where, svc, src, dst, rel, rel)
					}
				}
			}
		}
	})
}
