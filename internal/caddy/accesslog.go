package caddy

import (
	"slices"

	"github.com/Auugustocesar/caddy-view/internal/cfgpath"
)

// AccessLogFiles lists the files that the running configuration writes JSON logs to, sorted and without
// duplicates. These are the files caddy-view can follow. Loggers that write to stderr, or in another format, do not
// count. A logger that names no encoder is not trusted to write JSON.
func AccessLogFiles(cfg map[string]any) []string {
	files := []string{}
	for _, logger := range cfgpath.Map(cfgpath.Get(cfg, "logging", "logs")) {
		if cfgpath.Get(logger, "writer", "output") != "file" || cfgpath.Get(logger, "encoder", "format") != "json" {
			continue
		}
		name, ok := cfgpath.Get(logger, "writer", "filename").(string)
		if ok && name != "" && !slices.Contains(files, name) {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	return files
}
