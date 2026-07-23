package apiserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/subtle"
	"crypto/x509"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
	"github.com/gh0st-nemesis/nimbuscore/internal/identity"
)

type identityService struct {
	v1.UnimplementedIdentityServiceServer
	ca                *identity.CA
	joinToken         string
	svidTTL           time.Duration
	dataEncryptionKey []byte
}

func NewIdentityService(ca *identity.CA, joinToken string, svidTTL time.Duration, dataEncryptionKey []byte) v1.IdentityServiceServer {
	return &identityService{ca: ca, joinToken: joinToken, svidTTL: svidTTL, dataEncryptionKey: dataEncryptionKey}
}

func (svc *identityService) RequestSVID(_ context.Context, req *v1.RequestSVIDRequest) (*v1.RequestSVIDResponse, error) {
	if subtle.ConstantTimeCompare([]byte(req.GetJoinToken()), []byte(svc.joinToken)) != 1 {
		return nil, status.Error(codes.PermissionDenied, "invalid join token")
	}
	if req.GetNodeName() == "" {
		return nil, status.Error(codes.InvalidArgument, "node_name is required")
	}

	csr, err := x509.ParseCertificateRequest(req.GetCsrDer())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parse CSR: %v", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "CSR signature invalid: %v", err)
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "CSR must use an ECDSA public key")
	}

	var idPath string
	switch req.GetRole() {
	case v1.SVIDRole_SVID_ROLE_NODE:
		idPath = fmt.Sprintf("/node/%s", req.GetNodeName())
	case v1.SVIDRole_SVID_ROLE_CONTROL_PLANE:
		idPath = fmt.Sprintf("/control-plane/%s", req.GetNodeName())
	case v1.SVIDRole_SVID_ROLE_CLIENT:
		idPath = fmt.Sprintf("/client/%s", req.GetNodeName())
	default:
		return nil, status.Error(codes.InvalidArgument, "role must be SVID_ROLE_NODE, SVID_ROLE_CONTROL_PLANE, or SVID_ROLE_CLIENT")
	}

	id, err := spiffeid.FromPath(svc.ca.TrustDomain(), idPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "build SPIFFE ID: %v", err)
	}

	cert, err := svc.ca.IssueSVID(pub, id, svc.svidTTL)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issue SVID: %v", err)
	}

	resp := &v1.RequestSVIDResponse{
		CertDer:        cert.Raw,
		TrustBundleDer: svc.ca.Cert().Raw,
	}
	if req.GetRole() == v1.SVIDRole_SVID_ROLE_CONTROL_PLANE {
		resp.DataEncryptionKey = svc.dataEncryptionKey
	}
	return resp, nil
}
