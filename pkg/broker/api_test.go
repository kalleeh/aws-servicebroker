package broker

import (
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/ssm/ssmiface"
	"github.com/awslabs/aws-servicebroker/pkg/serviceinstance"
	osb "github.com/pmorie/go-open-service-broker-client/v2"
	"github.com/pmorie/osb-broker-lib/pkg/broker"
	"github.com/stretchr/testify/assert"
)

func TestGetCatalog(t *testing.T) {
	assertor := assert.New(t)

	opts := Options{
		TableName:          "testtable",
		S3Bucket:           "abucket",
		S3Region:           "us-east-1",
		S3Key:              "tempates/test",
		Region:             "us-east-1",
		BrokerID:           "awsservicebroker",
		PrescribeOverrides: false,
	}
	bl, _ := NewAWSBroker(opts, mockGetAwsSession, mockClients, mockGetAccountId, mockUpdateCatalog, mockPollUpdate)
	bl.listingcache.Set("__LISTINGS__", []ServiceNeedsUpdate{{Name: "test", Update: false}})

	expected := &broker.CatalogResponse{CatalogResponse: osb.CatalogResponse{}}
	actual, err := bl.GetCatalog(&broker.RequestContext{})
	assertor.Equal(nil, err, "err should be nil")
	assertor.Equal(expected, actual, "should return empty catalog")

	svc := osb.Service{
		ID:          "test-id",
		Name:        "test",
		Description: "blah",
		Plans: []osb.Plan{
			{
				ID:      "planid",
				Name:    "planname",
				Schemas: &osb.Schemas{},
			},
		},
	}

	bl.catalogcache.Set("test", svc)
	expected = &broker.CatalogResponse{CatalogResponse: osb.CatalogResponse{Services: []osb.Service{svc}}}
	actual, err = bl.GetCatalog(&broker.RequestContext{})
	assertor.Equal(nil, err, "err should be nil")
	assertor.Equal(expected, actual, "should return a single service matching the mock")
}

type mockDataStoreProvision struct{}

func (db mockDataStoreProvision) PutServiceDefinition(sd osb.Service) error { return nil }
func (db mockDataStoreProvision) GetParam(paramname string) (value string, err error) {
	return "some-value", nil
}
func (db mockDataStoreProvision) PutParam(paramname string, paramvalue string) error { return nil }
func (db mockDataStoreProvision) PutServiceInstance(si serviceinstance.ServiceInstance) error {
	return nil
}
func (db mockDataStoreProvision) GetServiceDefinition(serviceuuid string) (*osb.Service, error) {
	if serviceuuid == "test-service-id" {
		return &osb.Service{
			ID:   "test-service-id",
			Name: "test-service-name",
			Plans: []osb.Plan{
				{ID: "test-plan-id", Name: "test-plan-name", Schemas: &osb.Schemas{ServiceInstance: &osb.ServiceInstanceSchema{
					Create: &osb.InputParametersSchema{
						Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
							"req_param":      map[string]interface{}{"type": "string", "required": true},
							"override_param": map[string]interface{}{"type": "string"},
							"region":         map[string]interface{}{"type": "string"},
						},
							"$schema":  "http://json-schema.org/draft-06/schema#",
							"required": []interface{}{"req_param"},
						},
					},
				}}},
			},
		}, nil
	} else if serviceuuid == "err" {
		return nil, errors.New("test failure")
	} else if serviceuuid == "noplan" {
		return &osb.Service{}, nil
	}
	return nil, nil
}
func (db mockDataStoreProvision) GetServiceInstance(sid string) (*serviceinstance.ServiceInstance, error) {
	switch sid {
	case "err":
		return nil, errors.New("test failure")
	case "err-stack":
		return &serviceinstance.ServiceInstance{StackID: "err"}, nil
	case "exists":
		return &serviceinstance.ServiceInstance{StackID: "an-id"}, nil
	default:
		return nil, nil
	}
}
func (db mockDataStoreProvision) GetServiceBinding(id string) (*serviceinstance.ServiceBinding, error) {
	switch id {
	case "err":
		return nil, errors.New("test failure")
	case "exists":
		return &serviceinstance.ServiceBinding{
			ID:         "exists",
			InstanceID: "exists",
		}, nil
	default:
		return nil, nil
	}
}
func (db mockDataStoreProvision) PutServiceBinding(sb serviceinstance.ServiceBinding) error {
	return nil
}
func (db mockDataStoreProvision) DeleteServiceBinding(id string) error { return nil }

func TestProvision(t *testing.T) {
	assertor := assert.New(t)

	opts := Options{
		TableName:          "testtable",
		S3Bucket:           "abucket",
		S3Region:           "us-east-1",
		S3Key:              "tempates/test",
		Region:             "us-east-1",
		BrokerID:           "awsservicebroker",
		PrescribeOverrides: true,
	}
	bl, _ := NewAWSBroker(opts, mockGetAwsSession, mockClients, mockGetAccountId, mockUpdateCatalog, mockPollUpdate)
	bl.db.DataStorePort = mockDataStoreProvision{}
	bl.globalOverrides = map[string]string{"override_param": "some_value"}
	provReq := &osb.ProvisionRequest{
		InstanceID:          "test-instance-id",
		ServiceID:           "test-service-id",
		PlanID:              "test-plan-id",
		OriginatingIdentity: &osb.OriginatingIdentity{},
		AcceptsIncomplete:   true,
		Parameters: map[string]interface{}{
			"region":       "us-east-1",
			"anotherParam": "pval",
		},
	}
	reqContext := &broker.RequestContext{}

	expectedErr := newHTTPStatusCodeError(http.StatusBadRequest, "", "The parameter anotherParam is not available.")
	_, err := bl.Provision(provReq, reqContext)
	assertor.Equal(expectedErr, err, "should fail with missing parameter error")

	provReq.Parameters = map[string]interface{}{
		"region": "us-east-1",
	}
	expectedErr = newHTTPStatusCodeError(http.StatusBadRequest, "", "The parameter req_param is required.")
	_, err = bl.Provision(provReq, reqContext)
	assertor.Equal(expectedErr, err, "should fail with required parameter error")

	provReq.Parameters = map[string]interface{}{
		"region":    "us-east-1",
		"req_param": "pval",
	}
	expected := &broker.ProvisionResponse{ProvisionResponse: osb.ProvisionResponse{Async: true}}
	actual, err := bl.Provision(provReq, reqContext)
	assertor.Equal(nil, err, "err should be nil")
	assertor.Equal(expected, actual, "should return empty provision response")

	expectedErr = osb.HTTPStatusCodeError{
		StatusCode:   422,
		ErrorMessage: aws.String("AsyncRequired"),
		Description:  aws.String("This service plan requires client support for asynchronous service operations."),
	}
	_, err = bl.Provision(&osb.ProvisionRequest{AcceptsIncomplete: false}, &broker.RequestContext{})
	assertor.Equal(expectedErr, err, "err should be 422")

	expectedErr = newHTTPStatusCodeError(http.StatusBadRequest, "", "The service plan test-plan-id was not found.")
	provReq.ServiceID = "noplan"
	_, err = bl.Provision(provReq, reqContext)
	assertor.Equal(expectedErr, err, "should fail with missing plan error")

	expectedErr = newHTTPStatusCodeError(http.StatusInternalServerError, "", "Failed to get the service err: test failure")
	provReq.ServiceID = "err"
	_, err = bl.Provision(provReq, reqContext)
	assertor.Equal(expectedErr, err, "should fail with 500 test error")

	expectedErr = newHTTPStatusCodeError(http.StatusBadRequest, "", "The service nonexist was not found.")
	provReq.ServiceID = "nonexist"
	_, err = bl.Provision(provReq, reqContext)
	assertor.Equal(expectedErr, err, "should fail with 500 error")

	expectedErr = newHTTPStatusCodeError(http.StatusInternalServerError, "", "Failed to get the service instance err: test failure")
	provReq.ServiceID = "test-service-id"
	provReq.InstanceID = "err"
	_, err = bl.Provision(provReq, reqContext)
	assertor.Equal(expectedErr, err, "should fail with 500 error")

	expectedErr = newHTTPStatusCodeError(http.StatusConflict, "", "Service instance exists already exists but with different attributes.")
	provReq.ServiceID = "test-service-id"
	provReq.InstanceID = "exists"
	_, err = bl.Provision(provReq, reqContext)
	assertor.Equal(expectedErr, err, "should fail with 500 error")

}

func TestDeprovision(t *testing.T) {
	assertor := assert.New(t)

	opts := Options{
		TableName:          "testtable",
		S3Bucket:           "abucket",
		S3Region:           "us-east-1",
		S3Key:              "tempates/test",
		Region:             "us-east-1",
		BrokerID:           "awsservicebroker",
		PrescribeOverrides: true,
	}
	bl, _ := NewAWSBroker(opts, mockGetAwsSession, mockClients, mockGetAccountId, mockUpdateCatalog, mockPollUpdate)
	bl.db.DataStorePort = mockDataStoreProvision{}

	deprovReq := &osb.DeprovisionRequest{
		InstanceID:        "test-instance-id",
		AcceptsIncomplete: true,
	}
	reqContext := &broker.RequestContext{}

	expected := &broker.DeprovisionResponse{}
	actual, err := bl.Deprovision(deprovReq, reqContext)
	assertor.Equal(nil, err, "err should be nil")
	assertor.Equal(expected, actual, "should succeed even if stack is not in serviceInstance (was never created)")

	bl.accountId = "test"
	bl.secretkey = "testkey"

	deprovReq.InstanceID = "exists"
	expected.Async = true
	actual, err = bl.Deprovision(deprovReq, reqContext)
	assertor.Equal(nil, err, "err should be nil")
	assertor.Equal(expected, actual, "should succeed even if stack is not in serviceInstance (was never created)")

}

func TestLastOperation(t *testing.T) {
	assertor := assert.New(t)

	opts := Options{
		TableName:          "testtable",
		S3Bucket:           "abucket",
		S3Region:           "us-east-1",
		S3Key:              "tempates/test",
		Region:             "us-east-1",
		BrokerID:           "awsservicebroker",
		PrescribeOverrides: true,
	}

	bl, _ := NewAWSBroker(opts, mockGetAwsSession, mockClients, mockGetAccountId, mockUpdateCatalog, mockPollUpdate)
	bl.db.DataStorePort = mockDataStoreProvision{}

	loReq := &osb.LastOperationRequest{InstanceID: "test-instance-id"}
	reqContext := &broker.RequestContext{}
	msg := "CloudFormation stackid missing, chances are stack creation failed in an unexpected way"
	expected := &broker.LastOperationResponse{LastOperationResponse: osb.LastOperationResponse{State: "failed", Description: &msg}}
	actual, err := bl.LastOperation(loReq, reqContext)
	assertor.Equal(nil, err, "err should be nil")
	assertor.Equal(expected, actual, "should succeed even if stack is not in serviceInstance (was never created)")

	mockClients.NewCfn = func(sess *session.Session) CfnClient {
		return CfnClient{mockCfn{
			DescribeStacksResponse: cloudformation.DescribeStacksOutput{
				NextToken: nil,
				Stacks: []*cloudformation.Stack{
					{
						StackStatus: aws.String("CREATE_IN_PROGRESS"),
					},
				},
			},
		}}
	}
	bl, _ = NewAWSBroker(opts, mockGetAwsSession, mockClients, mockGetAccountId, mockUpdateCatalog, mockPollUpdate)
	bl.db.DataStorePort = mockDataStoreProvision{}
	expected = &broker.LastOperationResponse{LastOperationResponse: osb.LastOperationResponse{State: "in progress", Description: nil}}
	loReq.InstanceID = "exists"
	actual, err = bl.LastOperation(loReq, reqContext)
	assertor.Equal(nil, err, "err should be nil")
	assertor.Equal(expected, actual, "should succeed even if stack is not in serviceInstance (was never created)")

	mockClients.NewCfn = func(sess *session.Session) CfnClient {
		return CfnClient{mockCfn{
			DescribeStacksResponse: cloudformation.DescribeStacksOutput{
				NextToken: nil,
				Stacks: []*cloudformation.Stack{
					{
						StackStatus: aws.String("CREATE_FAILED"),
					},
				},
			},
		}}
	}
	bl, _ = NewAWSBroker(opts, mockGetAwsSession, mockClients, mockGetAccountId, mockUpdateCatalog, mockPollUpdate)
	bl.db.DataStorePort = mockDataStoreProvision{}
	expected = &broker.LastOperationResponse{LastOperationResponse: osb.LastOperationResponse{State: "failed", Description: nil}}
	loReq.InstanceID = "exists"
	actual, err = bl.LastOperation(loReq, reqContext)
	assertor.Equal(nil, err, "err should be nil")
	assertor.Equal(expected, actual, "should succeed even if stack is not in serviceInstance (was never created)")

	mockClients.NewCfn = func(sess *session.Session) CfnClient {
		return CfnClient{mockCfn{
			DescribeStacksResponse: cloudformation.DescribeStacksOutput{
				NextToken: nil,
				Stacks: []*cloudformation.Stack{
					{
						StackStatus: aws.String("CREATE_COMPLETE"),
					},
				},
			},
		}}
	}
	bl, _ = NewAWSBroker(opts, mockGetAwsSession, mockClients, mockGetAccountId, mockUpdateCatalog, mockPollUpdate)
	bl.db.DataStorePort = mockDataStoreProvision{}
	expected = &broker.LastOperationResponse{LastOperationResponse: osb.LastOperationResponse{State: "succeeded", Description: nil}}
	loReq.InstanceID = "exists"
	actual, err = bl.LastOperation(loReq, reqContext)
	assertor.Equal(nil, err, "err should be nil")
	assertor.Equal(expected, actual, "should succeed even if stack is not in serviceInstance (was never created)")
}

func toDescribeStacksOutput(outputs map[string]string) cloudformation.DescribeStacksOutput {
	var cfnOutputs []*cloudformation.Output
	for k, v := range outputs {
		cfnOutputs = append(cfnOutputs, &cloudformation.Output{
			OutputKey:   aws.String(k),
			OutputValue: aws.String(v),
		})
	}
	return cloudformation.DescribeStacksOutput{
		Stacks: []*cloudformation.Stack{
			{
				Outputs: cfnOutputs,
			},
		},
	}
}

func TestBind(t *testing.T) {
	tests := []struct {
		name           string
		request        *osb.BindRequest
		cfnOutputs     map[string]string
		ssmParams      map[string]string
		expectedCreds  map[string]interface{}
		expectedExists bool
		expectedErr    error
	}{
		{
			name: "unsupported_parameter",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "exists",
				ServiceID:  "test-service-id",
				Parameters: map[string]interface{}{"foo": "bar"},
			},
			expectedErr: newHTTPStatusCodeError(http.StatusBadRequest, "", "The parameter foo is not supported."),
		},
		{
			name: "error_getting_binding",
			request: &osb.BindRequest{
				BindingID:  "err",
				InstanceID: "exists",
				ServiceID:  "test-service-id",
			},
			expectedErr: newHTTPStatusCodeError(http.StatusInternalServerError, "", "Failed to get the service binding err: test failure"),
		},
		{
			name: "existing_binding",
			request: &osb.BindRequest{
				BindingID:  "exists",
				InstanceID: "exists",
				ServiceID:  "test-service-id",
			},
			expectedExists: true,
		},
		{
			name: "conflicting_binding",
			request: &osb.BindRequest{
				BindingID:  "exists",
				InstanceID: "foo",
				ServiceID:  "test-service-id",
			},
			expectedErr: newHTTPStatusCodeError(http.StatusConflict, "", "Service binding exists already exists but with different attributes."),
		},
		{
			name: "error_getting_service",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "exists",
				ServiceID:  "err",
			},
			expectedErr: newHTTPStatusCodeError(http.StatusInternalServerError, "", "Failed to get the service err: test failure"),
		},
		{
			name: "service_not_found",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "exists",
				ServiceID:  "foo",
			},
			expectedErr: newHTTPStatusCodeError(http.StatusBadRequest, "", "The service foo was not found."),
		},
		{
			name: "error_getting_instance",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "err",
				ServiceID:  "test-service-id",
			},
			expectedErr: newHTTPStatusCodeError(http.StatusInternalServerError, "", "Failed to get the service instance err: test failure"),
		},
		{
			name: "instance_not_found",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "foo",
				ServiceID:  "test-service-id",
			},
			expectedErr: newHTTPStatusCodeError(http.StatusBadRequest, "", "The service instance foo was not found."),
		},
		{
			name: "error_describing_stack",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "err-stack",
				ServiceID:  "test-service-id",
			},
			expectedErr: newHTTPStatusCodeError(http.StatusInternalServerError, "", "Failed to describe the CloudFormation stack err: test failure"),
		},
		{
			name: "error_getting_credentials",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "exists",
				ServiceID:  "test-service-id",
			},
			cfnOutputs: map[string]string{
				"BucketName":            "mystack-mybucket-kdwwxmddtr2g",
				"BucketAccessKeyId":     "ssm:/k8s/an-id/BucketAccessKeyId",
				"BucketSecretAccessKey": "ssm:/k8s/an-id/BucketSecretAccessKey",
			},
			ssmParams: map[string]string{
				"/k8s/an-id/BucketAccessKeyId": "foo",
			},
			expectedErr: newHTTPStatusCodeError(http.StatusInternalServerError, "", "Failed to get the credentials from CloudFormation stack an-id: invalid parameters: [/k8s/an-id/BucketSecretAccessKey]"),
		},
		{
			name: "get_credentials",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "exists",
				ServiceID:  "test-service-id",
			},
			cfnOutputs: map[string]string{
				"BucketName":            "mystack-mybucket-kdwwxmddtr2g",
				"BucketAccessKeyId":     "ssm:/k8s/an-id/BucketAccessKeyId",
				"BucketSecretAccessKey": "ssm:/k8s/an-id/BucketSecretAccessKey",
			},
			ssmParams: map[string]string{
				"/k8s/an-id/BucketAccessKeyId":     "foo",
				"/k8s/an-id/BucketSecretAccessKey": "bar",
			},
			expectedCreds: map[string]interface{}{
				"BUCKET_NAME":              "mystack-mybucket-kdwwxmddtr2g",
				"BUCKET_ACCESS_KEY_ID":     "foo",
				"BUCKET_SECRET_ACCESS_KEY": "bar",
			},
		},
		{
			name: "get_legacy_credentials",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "exists",
				ServiceID:  "test-service-id",
			},
			cfnOutputs: map[string]string{
				"BucketName":    "mystack-mybucket-kdwwxmddtr2g",
				"UserKeyId":     "/k8s/an-id/UserKeyId",
				"UserSecretKey": "/k8s/an-id/UserSecretKey",
			},
			ssmParams: map[string]string{
				"/k8s/an-id/UserKeyId":     "foo",
				"/k8s/an-id/UserSecretKey": "bar",
			},
			expectedCreds: map[string]interface{}{
				"BUCKET_NAME":                       "mystack-mybucket-kdwwxmddtr2g",
				"TEST-SERVICE-NAME_USER_KEY_ID":     "foo",
				"TEST-SERVICE-NAME_USER_SECRET_KEY": "bar",
			},
		},
		{
			name: "unsupported_scope",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "exists",
				ServiceID:  "test-service-id",
				Parameters: map[string]interface{}{
					"RoleName": "foo",
					"Scope":    "ReadOnly",
				},
			},
			expectedErr: newHTTPStatusCodeError(http.StatusBadRequest, "", "The CloudFormation stack an-id does not support binding with scope 'ReadOnly': output not found: PolicyArnReadOnly"),
		},
		{
			name: "error_attaching_role_policy",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "exists",
				ServiceID:  "test-service-id",
				Parameters: map[string]interface{}{
					"rOlEnAmE": "exists", // Also verify that RoleName is case-insensitive
				},
			},
			cfnOutputs: map[string]string{
				"PolicyArn": "err",
			},
			expectedErr: newHTTPStatusCodeError(http.StatusInternalServerError, "", "Failed to attach the policy err to role exists: test failure"),
		},
		{
			name: "attach_role_policy",
			request: &osb.BindRequest{
				BindingID:  "test-binding-id",
				InstanceID: "exists",
				ServiceID:  "test-service-id",
				Parameters: map[string]interface{}{
					"RoleName": "exists",
					"sCoPe":    "ReadWrite", // Also verify that Scope is case-insensitive
				},
			},
			cfnOutputs: map[string]string{
				"PolicyArnReadWrite": "exists",
			},
			expectedCreds: make(map[string]interface{}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clients := AwsClients{
				NewCfn: func(sess *session.Session) CfnClient {
					return CfnClient{
						Client: mockCfn{
							DescribeStacksResponse: toDescribeStacksOutput(tt.cfnOutputs),
						},
					}
				},
				NewDdb: mockAwsDdbClientGetter,
				NewIam: mockAwsIamClientGetter,
				NewS3:  mockAwsS3ClientGetter,
				NewSsm: func(sess *session.Session) ssmiface.SSMAPI {
					return &mockSSM{
						params: tt.ssmParams,
					}
				},
				NewSts: mockAwsStsClientGetter,
			}

			b, _ := NewAWSBroker(Options{}, mockGetAwsSession, clients, mockGetAccountId, mockUpdateCatalog, mockPollUpdate)
			b.db.DataStorePort = mockDataStoreProvision{}

			resp, err := b.Bind(tt.request, &broker.RequestContext{})
			if tt.expectedErr != nil {
				assert.EqualError(t, err, tt.expectedErr.Error())
			} else if assert.NoError(t, err) {
				assert.Equal(t, tt.expectedExists, resp.Exists)
				assert.Equal(t, tt.expectedCreds, resp.Credentials)
			}
		})
	}
}

func TestUnbind(t *testing.T) {
	assertor := assert.New(t)

	opts := Options{
		TableName:          "testtable",
		S3Bucket:           "abucket",
		S3Region:           "us-east-1",
		S3Key:              "tempates/test",
		Region:             "us-east-1",
		BrokerID:           "awsservicebroker",
		PrescribeOverrides: true,
	}
	bl, _ := NewAWSBroker(opts, mockGetAwsSession, mockClients, mockGetAccountId, mockUpdateCatalog, mockPollUpdate)
	bl.db.DataStorePort = mockDataStoreProvision{}

	unbindReq := &osb.UnbindRequest{BindingID: "exists"}
	reqContext := &broker.RequestContext{}

	expected := &broker.UnbindResponse{UnbindResponse: osb.UnbindResponse{}}
	actual, err := bl.Unbind(unbindReq, reqContext)
	assertor.Equal(nil, err, "err should be nil")
	assertor.Equal(expected, actual, "should succeed")

}