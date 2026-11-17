package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mhsanaei/3x-ui/v3/internal/hostops"
)

func (r *Remote) HostReport(ctx context.Context) (*hostops.Report, error) {
	response, err := r.do(ctx, http.MethodGet, "panel/api/server/hostRuntime", nil)
	if err != nil {
		return nil, err
	}
	if len(response.Obj) > 2<<20 {
		return nil, errors.New("host runtime report exceeds size limit")
	}
	var report hostops.Report
	if err := json.Unmarshal(response.Obj, &report); err != nil {
		return nil, err
	}
	if len(report.Observer.Records) > 500 {
		return nil, errors.New("too many host observations")
	}
	return &report, nil
}
