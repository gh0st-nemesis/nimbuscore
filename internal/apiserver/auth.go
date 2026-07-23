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
)

// unauthenticatedMethods lists RPCs reachable without a client SVID —
// the bootstrap enrollment path. Every other method goes through
// PeerSPIFFEID and the interceptor's expect matcher.
var unauthenticatedMethods = map[string]bool{
	"/nimbuscore.v1.IdentityService/RequestSVID": true,
}

// AuthInterceptor enforces "deny-by-default... aucun montage automatique
// de credentials" (design doc section 05) at the RPC layer: every method
// other than enrollment requires a client certificate whose SPIFFE ID
// satisfies expect. Fine-grained per-method RBAC is a Phase 3 concern
// (design doc roadmap); this only answers "is the caller a member of the
// cluster at all."
func AuthInterceptor(expect spiffeid.Matcher) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if unauthenticatedMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		id, err := PeerSPIFFEID(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "%v", err)
		}
		if err := expect(id); err != nil {
			return nil, status.Errorf(codes.PermissionDenied, "identity %s rejected: %v", id, err)
		}
		return handler(ctx, req)
	}
}

// PeerSPIFFEID extracts and validates the SPIFFE ID from the calling
// peer's verified TLS client certificate. Handlers that need finer
// checks than AuthInterceptor's blanket membership test (e.g.
// AdminService.JoinRaft restricting to control-plane replicas) call this
// directly.
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
