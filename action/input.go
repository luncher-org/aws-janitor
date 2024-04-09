package action

import (
	"fmt"

	"github.com/caarlos0/env/v9"
	"go.uber.org/multierr"
)

type Input struct {
	Regions        string `env:"INPUT_REGIONS"`
	AllowAllRegion bool   `env:"INPUT_ALLOW-ALL-REGIONS"`
	Commit         bool   `env:"INPUT_COMMIT"`
	IgnoreTag      string `env:"INPUT_IGNORE-TAG"`
}

// NewInput creates a new input from the environment variables.
func NewInput() (*Input, error) {
	input := &Input{}
	if err := env.Parse(input); err != nil {
		return nil, fmt.Errorf("parsing environment variables: %w", err)
	}

	return input, nil
}

func (i *Input) Validate() error {
	var err error

	if i.Regions == "" {
		err = multierr.Append(err, ErrRegionsRequired)
	}

	if i.Regions == "*" && !i.AllowAllRegion {
		err = multierr.Append(err, ErrAllRegionsNotAllowed)
	}

	return err
}
