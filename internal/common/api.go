package common

type Release struct {
	Owner        string   `mapstructure:"owner"`
	Repo         string   `mapstructure:"repo"`
	Assets       []string `mapstructure:"assets"`
	HelmChart    string   `mapstructure:"chart"`
	Filter       []string `mapstructure:"filter"`
	Replacements string   `mapstructure:"replacements"` // must be kept as a string for yaml unmarshalling into kustomize replacements...
}

type Config struct {
	Log struct {
		Level string `mapstructure:"level"`
	} `mapstructure:"log"`

	Releases []Release `mapstructure:"githubReleases"`
}
