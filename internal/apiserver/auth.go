package apiserver

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"github.com/gh0st-nemesis/nimbuscore/internal/rbac"
)

var unauthenticatedMethods = map[string]bool{
	"/nimbuscore.v1.IdentityService/RequestSVID": true,
}

func AuthInterceptor(expectTrustDomain spiffeid.Matcher, authz *rbac.Authorizer) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if unauthenticatedMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		id, err := PeerSPIFFEID(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "%v", err)
		}
		if err := expectTrustDomain(id); err != nil {
			return nil, status.Errorf(codes.PermissionDenied, "identity %s rejected: %v", id, err)
		}

		rv, ok := methodResourceVerb[info.FullMethod]
		if !ok {
			return nil, status.Errorf(codes.PermissionDenied, "method %s has no RBAC mapping — denied by default", info.FullMethod)
		}
		if !authz.Allow(id.Path(), rv.Resource, rv.Verb) {
			return nil, status.Errorf(codes.PermissionDenied, "identity %s is not authorized to %s %s", id, rv.Verb, rv.Resource)
		}

		return handler(ctx, req)
	}
}

func PeerSPIFFEID(ctx context.Context) (spiffeid.ID, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return spiffeid.ID{}, errors.New("no peer information on context")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return spiffeid.ID{}, errors.New("connection is not TLS")
	}
	if len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return spiffeid.ID{}, errors.New("no client certificate presented")
	}

	leaf := tlsInfo.State.VerifiedChains[0][0]
	if len(leaf.URIs) != 1 {
		return spiffeid.ID{}, errors.New("client certificate must carry exactly one URI SAN")
	}
	return spiffeid.FromURI(leaf.URIs[0])
}
