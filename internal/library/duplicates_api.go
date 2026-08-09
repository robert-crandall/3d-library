package library

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/robert-crandall/go-home-server/apisec"
)

// duplicatesOutput is the whole page: the groups and the scan status together.
//
// One response rather than two endpoints because the page's states are defined
// by both at once - an empty group list means "no duplicates" only when the
// status says nothing is pending, and two round trips could disagree about that
// in a way the shared snapshot inside Duplicates exists to prevent.
type duplicatesOutput struct {
	Body Duplicates
}

type scanOutput struct {
	Body ScanStatus
}

// registerDuplicates mounts the two routes the duplicates page needs.
//
// The scan is a POST that returns immediately and a GET that is polled, rather
// than one long request. Hashing a few hundred large files is minutes of disk
// I/O: a request that long cannot report progress, and anything in front of it
// would time it out.
func registerDuplicates(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "list-duplicates",
		Summary:     "Files stored more than once",
		Description: "Groups of files with identical contents, with how much " +
			"deleting all but one copy of each would free, plus the state of " +
			"the scan that found them.",
		Method:   http.MethodGet,
		Path:     "/api/duplicates",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized},
		Security: apisec.User(api),
	}, func(ctx context.Context, _ *struct{}) (*duplicatesOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		found, err := svc.Duplicates(ctx, userID)
		if err != nil {
			return nil, internalError("could not list duplicates", err)
		}
		// A nil slice would serialize as null, and the client's "no duplicates"
		// branch reads a length.
		if found.Groups == nil {
			found.Groups = []DuplicateGroup{}
		}
		return &duplicatesOutput{Body: found}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "scan-duplicates",
		Summary:     "Scan for duplicate files",
		Description: "Hashes every file whose size is shared with another file " +
			"and stores the result, so later scans skip it. Returns as soon as " +
			"the scan starts; poll the duplicates list for progress. Scanning " +
			"while a scan is already running does nothing.",
		Method:   http.MethodPost,
		Path:     "/api/duplicates/scan",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized},
		Security: apisec.User(api),
	}, func(ctx context.Context, _ *struct{}) (*scanOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		status, err := svc.StartDuplicateScan(ctx, userID)
		if err != nil {
			return nil, internalError("could not start the scan", err)
		}
		return &scanOutput{Body: status}, nil
	})
}
