// Package ec2 provides a minimal EC2 API stub for Nimbus.
// Only DescribeSubnets is implemented — enough for the Pulumi AWS provider to
// read subnet metadata after creating EFS mount targets.
// All other EC2 actions return HTTP 501.
package ec2

import (
	"fmt"
	"net/http"
	"strings"
)

// Service implements a minimal EC2 emulator.
type Service struct{}

// New creates a new EC2 service.
func New() *Service { return &Service{} }

func (s *Service) Name() string { return "ec2" }

// Detect identifies EC2 query-protocol requests:
// POST / with Content-Type application/x-www-form-urlencoded.
func (s *Service) Detect(r *http.Request) bool {
	return r.Method == http.MethodPost &&
		r.URL.Path == "/" &&
		strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	action := r.FormValue("Action")
	switch action {
	case "DescribeSubnets":
		s.describeSubnets(w, r)
	default:
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(http.StatusNotImplemented)
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
			`<Response><Errors><Error><Code>UnsupportedOperation</Code>`+
			`<Message>EC2 action not emulated: %s</Message></Error></Errors></Response>`, action)
	}
}

// describeSubnets returns a stub subnet entry for each requested subnet ID.
// The Pulumi AWS provider calls this after creating an EFS mount target to
// populate VpcId and AvailabilityZoneName in the resource state.
func (s *Service) describeSubnets(w http.ResponseWriter, r *http.Request) {
	// Collect subnet IDs from Filter.N.Value.M (filter name = subnet-id)
	// or from SubnetId.N parameters.
	ids := collectSubnetIDs(r)

	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<DescribeSubnetsResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <requestId>nimbus-ec2-stub</requestId>
  <subnetSet>`)
	for i, id := range ids {
		az := fmt.Sprintf("us-east-1%c", 'a'+byte(i%3))
		fmt.Fprintf(w, `
    <item>
      <subnetId>%s</subnetId>
      <state>available</state>
      <vpcId>vpc-00000001</vpcId>
      <cidrBlock>10.0.%d.0/24</cidrBlock>
      <availableIpAddressCount>251</availableIpAddressCount>
      <availabilityZone>%s</availabilityZone>
      <availabilityZoneId>use1-az%d</availabilityZoneId>
      <defaultForAz>false</defaultForAz>
      <mapPublicIpOnLaunch>false</mapPublicIpOnLaunch>
      <ownerId>000000000000</ownerId>
    </item>`, id, i+1, az, i+1)
	}
	fmt.Fprint(w, `
  </subnetSet>
</DescribeSubnetsResponse>`)
}

// collectSubnetIDs extracts subnet IDs from the parsed EC2 form parameters.
// Handles both SubnetId.N and Filter-based (Filter.N.Name=subnet-id) forms.
func collectSubnetIDs(r *http.Request) []string {
	var ids []string
	seen := map[string]bool{}

	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	// SubnetId.1, SubnetId.2, ...
	for i := 1; ; i++ {
		v := r.FormValue(fmt.Sprintf("SubnetId.%d", i))
		if v == "" {
			break
		}
		add(v)
	}

	// Filter.N.Name=subnet-id, Filter.N.Value.M=<id>
	for fi := 1; ; fi++ {
		name := r.FormValue(fmt.Sprintf("Filter.%d.Name", fi))
		if name == "" {
			break
		}
		if name != "subnet-id" {
			continue
		}
		for vi := 1; ; vi++ {
			v := r.FormValue(fmt.Sprintf("Filter.%d.Value.%d", fi, vi))
			if v == "" {
				break
			}
			add(v)
		}
	}

	return ids
}
