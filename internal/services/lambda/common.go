package lambda

import (
	"fmt"

	"github.com/nimbus-local/nimbus/internal/uid"
)

func (s *Service) functionARN(name string) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s", s.region, s.account, name)
}

func newRevisionID() string {
	return uid.New()
}
