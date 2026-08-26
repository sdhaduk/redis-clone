package store

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"time"
)

type policyType int

const (
	Always policyType = iota
	Everysec
	No
)

func ParsePolicy(s string) (policyType, error) {
	switch s {
	case "always":
		return Always, nil
	case "everysec":
		return Everysec, nil
	case "no":
		return No, nil
	default:
		return 0, fmt.Errorf("invalid appendfsync policy %q", s)
	}
}

type AOF struct {
	file   *os.File
	policy policyType
	writes chan []byte
}

func NewAOF(path string, policy policyType) (*AOF, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	aof := &AOF{file: file, policy: policy, writes: make(chan []byte, 512)}

	if aof.policy != Always {
		go aof.run()
	}
	return aof, nil
}

func (aof *AOF) run() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	f := bufio.NewWriter(aof.file)
	for {
		select {
		case msg, ok := <-aof.writes:
			if !ok {
				err := f.Flush()
				if err != nil {
					log.Fatal(err)
				}
				err = aof.file.Sync()
				if err != nil {
					log.Fatal(err)
				}
				return
			}
			_, err := f.Write(msg)
			if err != nil {
				log.Fatal(err)
			}
		case <-ticker.C:
			err := f.Flush()
			if err != nil {
				log.Fatal(err)
			}
			if aof.policy != No {
				err := aof.file.Sync()
				if err != nil {
					log.Fatal(err)
				}
			}
		}
	}
}

func (aof *AOF) Append(cmd []byte) error {
	switch aof.policy {
	case Always:
		_, err := aof.file.Write(cmd)
		if err != nil {
			return err
		}
		err = aof.file.Sync()
		if err != nil {
			return err
		}
		return nil

	case Everysec:
		aof.writes <- cmd
		return nil

	case No:
		aof.writes <- cmd
		return nil

	default:
		return errors.New("invalid policy type")
	}
}
