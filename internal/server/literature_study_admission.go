package server

import (
	"context"
	"errors"
	"sync"
)

var errLiteratureStudyBusy = errors.New("study overview generation is busy")

// One finite admission owner per Server, including a zero-value Server. This
// is not a queue or execution lifetime: the synchronous request still owns it.
type literatureStudyPermitPool struct {
	once  sync.Once
	slots chan struct{}
}

func (s *Server) acquireLiteratureStudyPermit(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.literatureStudyPermits.once.Do(func() { s.literatureStudyPermits.slots = make(chan struct{}, 4) })
	slots := s.literatureStudyPermits.slots
	select {
	case slots <- struct{}{}:
		var once sync.Once
		release := func() { once.Do(func() { <-slots }) }
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	default:
		return nil, errLiteratureStudyBusy
	}
}
