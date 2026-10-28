package utils

import (
	"fmt"
	"strings"
)

func ParserUUID(value string) (id string, err error) {
	id = strings.TrimSpace(value)
	if id == "" {
		return "", fmt.Errorf("empty uuid value")
	}
	return id, nil
}
