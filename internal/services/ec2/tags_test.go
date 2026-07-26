package ec2

import (
	"strings"
	"testing"
)

// SDK clients tag a resource as part of its create call, via
// TagSpecification.N.Tag.M rather than the flat Tag.N form. Dropping those tags
// leaves every Describe reporting an empty tagSet, which reads as "no tags set"
// and produces a change on every plan.
func TestCreate_StoresTagSpecificationTags(t *testing.T) {
	tests := []struct {
		name     string
		action   string
		describe string
		resource string
	}{
		{"vpc", "Action=CreateVpc&CidrBlock=10.0.0.0/16", "Action=DescribeVpcs", "vpc"},
		{"subnet", "Action=CreateSubnet&VpcId=vpc-1&CidrBlock=10.0.1.0/24", "Action=DescribeSubnets", "subnet"},
		{"internet gateway", "Action=CreateInternetGateway", "Action=DescribeInternetGateways", "igw"},
		{"security group", "Action=CreateSecurityGroup&GroupName=n&GroupDescription=d&VpcId=vpc-1", "Action=DescribeSecurityGroups", "sg"},
		{"route table", "Action=CreateRouteTable&VpcId=vpc-1", "Action=DescribeRouteTables", "rtb"},
	}

	const tagSpec = "&TagSpecification.1.ResourceType=%s" +
		"&TagSpecification.1.Tag.1.Key=Name&TagSpecification.1.Tag.1.Value=tagged" +
		"&TagSpecification.1.Tag.2.Key=env&TagSpecification.1.Tag.2.Value=smoke"

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newSvc()
			must200(t, ec2Req(t, svc, tc.action+strings.Replace(tagSpec, "%s", tc.resource, 1)))

			body := must200(t, ec2Req(t, svc, tc.describe))
			mustContain(t, body, "<key>Name</key><value>tagged</value>")
			mustContain(t, body, "<key>env</key><value>smoke</value>")
		})
	}
}

// The flat form still has to work — callers that tag separately use it, and the
// tag helpers apply it after creation.
func TestCreate_StillAcceptsFlatTagForm(t *testing.T) {
	svc := newSvc()
	must200(t, ec2Req(t, svc,
		"Action=CreateVpc&CidrBlock=10.0.0.0/16&Tag.1.Key=Name&Tag.1.Value=flat"))

	mustContain(t, must200(t, ec2Req(t, svc, "Action=DescribeVpcs")),
		"<key>Name</key><value>flat</value>")
}

func TestCreate_NoTagsLeavesEmptyTagSet(t *testing.T) {
	svc := newSvc()
	must200(t, ec2Req(t, svc, "Action=CreateVpc&CidrBlock=10.0.0.0/16"))

	mustContain(t, must200(t, ec2Req(t, svc, "Action=DescribeVpcs")), "<tagSet/>")
}
