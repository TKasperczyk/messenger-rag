package ragconfig

import "strings"

// LoadFromFlagOrDir loads the config from cfgPath if provided, otherwise searches
// for rag.yaml starting from dir (walking up parent directories).
func LoadFromFlagOrDir(cfgPath string, dir string) (*Config, error) {
	if strings.TrimSpace(cfgPath) != "" {
		return Load(cfgPath)
	}
	return LoadFromDir(dir)
}
