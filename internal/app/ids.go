package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

type IDFactory struct {
	counter atomic.Int64
}

func (f *IDFactory) OperationID(site string, at time.Time) string {
	n := f.counter.Add(1)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", site, at.UTC().Format(time.RFC3339Nano), n)))
	return "op-" + hex.EncodeToString(sum[:])[:16]
}

func (f *IDFactory) EventType(prefix string) string {
	n := f.counter.Add(1)
	return fmt.Sprintf("%s-%06d", prefix, n)
}
