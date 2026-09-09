package grimoire

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Boring bool `json:"boring"`
}

func configuredOptions(paths Paths) (Config, error) {
	body, err := os.ReadFile(paths.ConfigFile())
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var config Config
	if err := json.Unmarshal(body, &config); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return config, nil
}

func (c *CLI) configure(args []string) (int, error) {
	if len(args) == 0 {
		if c.configErr != nil {
			return 1, c.configErr
		}
		fmt.Fprintf(c.Out, "boring=%t\n", c.Config.Boring)
		return 0, nil
	}
	if args[0] != "boring" || len(args) > 2 {
		return 1, fmt.Errorf("usage: grimoire config boring [true|false]")
	}
	value := true
	if len(args) == 2 {
		switch args[1] {
		case "true":
			value = true
		case "false":
			value = false
		default:
			return 1, fmt.Errorf("boring must be true or false")
		}
	}
	updated := c.Config
	updated.Boring = value
	unlock, err := lockBindings(c.Paths)
	if err != nil {
		return 1, err
	}
	defer unlock()
	if err := writeJSONFile(c.Paths.ConfigFile(), updated); err != nil {
		return 1, fmt.Errorf("save config: %w", err)
	}
	c.Config = updated
	c.configErr = nil
	fmt.Fprintf(c.Out, "boring=%t\n", value)
	return 0, nil
}
