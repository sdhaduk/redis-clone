package store

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"redis-clone/resp"
	"strconv"
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
	path   string
	newFile chan *os.File
}

func replayFrom(r io.Reader, store map[string]entry) {
	reader := bufio.NewReader(r)
	cmdsLoaded := 0
	for {
		val, err := resp.Decode(reader)
		if err == io.EOF {
			return
		}
		if err != nil {
			log.Printf("Error: could not decode the log file; %d commands were executed", cmdsLoaded)
			return
		}
		args, err := val.Args()
		if err != nil {
			log.Printf("Error: could not decode bytes into arguments; %d commands were executed", cmdsLoaded)
			return
		}
		dispatch(store, args)
		cmdsLoaded += 1
	}
}

func dumpCommands(store map[string]entry) [][]string {
	cmds := [][]string{}
	for k := range store {
		ent, ok := lookup(store, k)
		if !ok {
			continue
		}
		switch ent.Kind {
		case String:
			cmds = append(cmds, []string{"SET", k, ent.Str})
		
		case List:
			cmd := []string{"RPUSH", k}
			cmd = append(cmd, ent.List...)
			cmds = append(cmds, cmd)
		
		case Hash:
			cmd := []string{"HSET", k}
			for hk, hv := range ent.Hash {
				cmd = append(cmd, hk, hv)
			}
			cmds = append(cmds, cmd)
		
		case Set:
			cmd := []string{"SADD", k}
			for sk := range ent.Set {
				cmd = append(cmd, sk)
			}
			cmds = append(cmds, cmd)
		
		case ZSet:
			cmd := []string{"ZADD", k}
			for zk, zv := range ent.ZSet.members {
				cmd = append(cmd, strconv.FormatFloat(zv, 'g', -1, 64), zk)
			}
			cmds = append(cmds, cmd)
		}
		if !ent.expiresAt.IsZero() {
			deadline := strconv.FormatInt(ent.expiresAt.UnixMilli(), 10)
			cmds = append(cmds, []string{"PEXPIREAT", k, deadline})
		}
	}

	return cmds
}

func NewAOF(path string, policy policyType) (*AOF, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	aof := &AOF{file: file, policy: policy, writes: make(chan []byte, 512), path: path, newFile: make(chan *os.File)}
	go aof.run()

	return aof, nil
}

func (aof *AOF) rewrite(store map[string]entry) error {
	tempF, err := os.CreateTemp(filepath.Dir(aof.path), "temp_aof.*.aof")
	if err != nil {
		return fmt.Errorf("Error creating temp file: %v", err)
	}
	defer os.Remove(tempF.Name())

	tf := bufio.NewWriter(tempF)
	cmds := dumpCommands(store)
	for _, cmd := range cmds {
		res := resp.Command(cmd)
		_, err := tf.Write(res.Encode())
		if err != nil {
			return fmt.Errorf("Error encoding cmd: %v", err)
		}
	}
	err = tf.Flush()
	if err != nil {
		return fmt.Errorf("Error flushing buffer after dunp: %v", err)
	}
	err = tempF.Sync()
	if err != nil {
		return fmt.Errorf("Error syncing temp file: %v", err)
	}
	err = tempF.Close()
	if err != nil {
		return fmt.Errorf("Error closing temp file: %v", err)	
	}
	err = os.Rename(tempF.Name(), aof.path)
	if err != nil {
		return fmt.Errorf("Error renaming file: %v", err)
	}
	newFile, err := os.OpenFile(aof.path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
    	log.Fatalf("could not reopen aof after rewrite: %v", err)
	}	
	aof.newFile <- newFile
	<- aof.newFile
	return nil
}

func (aof *AOF) load(store map[string]entry) {
	f, err := os.Open(aof.path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	replayFrom(f, store)
}

func (aof *AOF) run() {
	var tick <- chan time.Time
	if aof.policy != Always {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		tick = ticker.C
	}
	

	f := bufio.NewWriter(aof.file)
	for {
		select {
		case msg, ok := <- aof.writes:
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
		
		case msg, ok := <- aof.newFile:
			if !ok {
				return
			}
			
			oldFile := aof.file
			err := oldFile.Close()
			if err != nil {
				log.Fatalf("Error closing old file during rewrite: %v", err)
			}
			aof.file = msg
			f = bufio.NewWriter(aof.file)

			drain:
			for {
			    select {
			    case <-aof.writes:
			    default:
			        break drain
			    }
			}
			aof.newFile <- aof.file

			
		case <-tick:
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
