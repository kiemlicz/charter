package common

type Release struct {
	Owner     string   `mapstructure:"owner"`
	Repo      string   `mapstructure:"repo"`
	Assets    []string `mapstructure:"assets"`
	HelmChart string   `mapstructure:"chart"`
}
