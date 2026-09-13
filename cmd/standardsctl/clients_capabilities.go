package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientsetup"
)

func reportClientCapabilities(args []string) error {
	if len(args) != 0 {
		return errors.New("clients capabilities accepts no arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := clientsetup.Capabilities(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode client capabilities: %w", err)
	}
	data = append(data, '\n')
	_, err = os.Stdout.Write(data)
	return err
}
