package deploy

import "github.com/mwiget/ocibnkctl/kit"

// mustRender renders a kit template for renderers whose signature has no
// error. A failure means a missing embedded file or an unsupplied
// variable, a programming error the package tests catch, so it panics.
func mustRender(path string, vars map[string]string) string {
	out, err := kit.Render(path, vars)
	if err != nil {
		panic("deploy: " + err.Error())
	}
	return out
}
