package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	TmpFileFolder  = "/tmp"
	TmpFilePreffix = "flow-cache-file"
	TmpFilePerm    = 0444
)

type CacheFile struct {
	path string
	ttl  time.Duration
}

func New(path string, ttl time.Duration) (*CacheFile, error) {
	return &CacheFile{
		path: path,
		ttl:  ttl,
	}, nil
}

func (f *CacheFile) Read() ([]byte, error) {
	valid, thisIsWhy := f.IsValid()
	if !valid {
		return nil, thisIsWhy
	}

	data, err := ioutil.ReadFile(f.path)
	if err != nil {
		log.Debugf("Failed to read from %s: %s", f.path, err)
	}

	return data, nil
}

func (f *CacheFile) Consolidate(data []byte) error {
	tmpFile, err := ioutil.TempFile(TmpFileFolder, TmpFileFolder)
	if err != nil {
		return err
	}

	writeErr := ioutil.WriteFile(tmpFile.Name(), data, TmpFilePerm)
	if writeErr != nil {
		defer os.Remove(tmpFile.Name())
		return writeErr
	}

	renameErr := os.Rename(tmpFile.Name(), f.path)
	if err != nil {
		defer os.Remove(tmpFile.Name())
		return renameErr
	}

	return nil
}

func (f *CacheFile) IsValid() (bool, error) {
	stat, err := os.Stat(f.path)

	if os.IsNotExist(err) {
		log.Debugf("File %s does not exist, can't read", f.path)
		return false, err
	} else if err != nil {
		log.Debugf("Failed to stat file %s: %s", f.path, err)
		return false, err
	}

	modTime := stat.ModTime()
	modSince := time.Now().Sub(modTime)
	if modSince > f.ttl {
		errMsg := fmt.Sprintf("File %s has expired (TTL: %f, modified: %f seconds ago)",
			f.path, f.ttl.Seconds(), modSince.Seconds())
		log.Debugf(errMsg)
		return false, fmt.Errorf(errMsg)
	}
	return true, nil
}

func (f *CacheFile) Invalidate() error {
	//TODO
	return nil
}