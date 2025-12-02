package main

import (
	"context"
	"fmt"
)

// +check
func (m *GoIngressDev) IsFmted(ctx context.Context) error {
	if empty, err := m.Fmt(ctx).IsEmpty(ctx); err != nil {
		return err
	} else if !empty {
		return fmt.Errorf("source is not formatted (run `dagger call fmt`)")
	}

	return nil
}

// +check
func (m *GoIngressDev) IsGenerated(ctx context.Context) error {
	if empty, err := m.Generate(ctx).IsEmpty(ctx); err != nil {
		return err
	} else if !empty {
		return fmt.Errorf("source is not generated (run `dagger call generate`)")
	}

	return nil
}
