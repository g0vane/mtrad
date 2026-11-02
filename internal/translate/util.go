package translate

import (
	"errors"
	"os"
	"strings"
)

func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist) || (err != nil && strings.Contains(err.Error(), "no such file"))
}
