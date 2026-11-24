//go:build !linux

package helper

import "errors"

func withTailnetKeyFile(string, string, func() error) error {
	return errors.New("nur unter Linux")
}

func (s *Server) removeStaleTailnetKey() {}
