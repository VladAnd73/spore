package widget

type Config struct {
	Name string
}

type Widget struct {
	cfg *Config
}

func (w *Widget) Render() *string {
	if w.cfg == nil {
		return nil
	}
	return &w.cfg.Name
}
