package library

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/robert-crandall/go-home-server/apisec"
)

// CurrentUserFunc resolves the signed-in user from a request context. It is
// injected rather than imported so this package does not depend on the auth
// package's shape.
type CurrentUserFunc func(ctx context.Context) (int64, error)

// Register mounts the library routes.
func Register(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	registerCreate(api, svc, currentUser)
	registerAddFile(api, svc, currentUser)
	registerList(api, svc, currentUser)
	registerGet(api, svc, currentUser)
}

// uploadInput carries the streaming multipart reader from the resolver to the
// handler. The field is unexported and has no huma tags, so huma neither
// validates nor documents it - it is a courier, not part of the contract.
type uploadInput struct {
	Name string `query:"name" required:"true" maxLength:"200" doc:"The model's display name"`

	parts *multipart.Reader
}

// Resolve builds the multipart reader from the live request.
//
// This has to be a resolver rather than the handler's own work because huma
// hands a handler ctx.Context(), not the huma.Context, so a handler cannot
// reach the underlying *http.Request. Resolvers do get the huma.Context.
func (in *uploadInput) Resolve(ctx huma.Context) []error {
	parts, errs := multipartReader(ctx)
	in.parts = parts
	return errs
}

func multipartReader(ctx huma.Context) (*multipart.Reader, []error) {
	r, _ := humachi.Unwrap(ctx)

	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return nil, []error{&huma.ErrorDetail{
			Location: "header.Content-Type",
			Message:  "expected multipart/form-data",
		}}
	}
	boundary, ok := params["boundary"]
	if !ok {
		return nil, []error{&huma.ErrorDetail{
			Location: "header.Content-Type",
			Message:  "multipart/form-data is missing its boundary parameter",
		}}
	}
	return multipart.NewReader(r.Body, boundary), nil
}

func registerCreate(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "create-model",
		Summary:     "Upload a model",
		Description: "Creates a named model from a single uploaded file. Send " +
			"the remaining files of a multi-file model to " +
			"POST /api/models/{id}/files, one request each. The model is " +
			"created by its first file so that a failed upload leaves no " +
			"empty entry in the library.",
		Method:      http.MethodPost,
		Path:        "/api/models",
		Tags:        []string{"library"},
		Errors:      []int{http.StatusUnauthorized, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity},
		Security:    apisec.User(api),
		Middlewares: huma.Middlewares{svc.guardUpload(api, currentUser)},

		// The schema is deliberately opaque. huma buffers and unmarshals the
		// whole request body whenever an operation declares a request body
		// whose schema is anything *other* than string/binary - so a
		// descriptive object schema here would silently read a multi-gigabyte
		// upload into memory before the handler ever ran. Leaving RequestBody
		// unset instead would make the committed spec claim this endpoint takes
		// no body at all.
		RequestBody: &huma.RequestBody{
			Required: true,
			Description: "multipart/form-data with exactly one part named \"file\". " +
				"Described as opaque binary so the server can stream it; clients " +
				"send a FormData, not a string.",
			Content: map[string]*huma.MediaType{
				"multipart/form-data": {
					Schema: &huma.Schema{Type: "string", Format: "binary"},
				},
			},
		},
	}, func(ctx context.Context, in *uploadInput) (*modelOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		model, err := svc.Create(ctx, userID, in.Name, in.parts)
		if err != nil {
			return nil, uploadError(err)
		}
		return &modelOutput{Status: http.StatusCreated, Body: model}, nil
	})
}

// addFileInput is uploadInput's sibling for an existing model. The two cannot
// share a struct because this one takes the model from the path and no name.
type addFileInput struct {
	ID int64 `path:"id"`

	parts *multipart.Reader
}

func (in *addFileInput) Resolve(ctx huma.Context) []error {
	parts, errs := multipartReader(ctx)
	in.parts = parts
	return errs
}

type fileOutput struct {
	Status int
	Body   File
}

func registerAddFile(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "add-model-file",
		Summary:     "Add a file to a model",
		Description: "Uploads one more file into an existing model. One request " +
			"carries one file.",
		Method:      http.MethodPost,
		Path:        "/api/models/{id}/files",
		Tags:        []string{"library"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity},
		Security:    apisec.User(api),
		Middlewares: huma.Middlewares{svc.guardUpload(api, currentUser)},

		// Opaque for the same reason as create-model; see there.
		RequestBody: &huma.RequestBody{
			Required:    true,
			Description: "multipart/form-data with exactly one part named \"file\".",
			Content: map[string]*huma.MediaType{
				"multipart/form-data": {
					Schema: &huma.Schema{Type: "string", Format: "binary"},
				},
			},
		},
	}, func(ctx context.Context, in *addFileInput) (*fileOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		file, err := svc.AddFile(ctx, userID, in.ID, in.parts)
		if errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("model not found")
		}
		if err != nil {
			return nil, uploadError(err)
		}
		return &fileOutput{Status: http.StatusCreated, Body: file}, nil
	})
}

// uploadError maps a Create failure onto a status. Anything unrecognized falls
// through to huma's 500, which is the right default: an unmapped error is a
// server bug, not something the client can fix by retrying differently.
func uploadError(err error) error {
	var maxBytes *http.MaxBytesError
	switch {
	case errors.Is(err, ErrTooLarge):
		return huma.Error413RequestEntityTooLarge(err.Error())
	case errors.As(err, &maxBytes):
		return huma.Error413RequestEntityTooLarge(
			fmt.Sprintf("upload is too large (max %d bytes)", maxBytes.Limit))
	case errors.Is(err, errInvalid):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		return err
	}
}

// guardUpload does the two things the resolver cannot, because both need the
// service's own limits and the raw ResponseWriter.
func (s *Service) guardUpload(api huma.API, currentUser CurrentUserFunc) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		r, w := humachi.Unwrap(ctx)

		// Authenticate before touching the body. Without this an anonymous
		// caller could stream gigabytes at the disk before the handler's own
		// check ran, since the handler only runs after the resolver.
		if _, err := currentUser(r.Context()); err != nil {
			huma.WriteErr(api, ctx, http.StatusUnauthorized, "authentication required")
			return
		}

		// The server's own deadlines are 30s read / 60s write and cannot be
		// configured. net/http arms the *write* deadline as soon as the request
		// headers are read, so it expires while the body is still arriving:
		// 500 MB inside 60s needs 8.5 MB/s sustained, which a phone on home
		// WiFi does not do. Both are widened for this one request.
		//
		// If the deadlines cannot be reset the upload proceeds anyway rather
		// than failing: the worst case is the timeout that would have happened
		// without this code at all.
		rc := http.NewResponseController(w)
		deadline := time.Now().Add(UploadTimeout)
		_ = rc.SetReadDeadline(deadline)
		_ = rc.SetWriteDeadline(deadline)

		// A total cap on the body. The per-file limit and the file count bound
		// a well-formed request, but mime/multipart discards arbitrarily many
		// preamble lines before the first part, so a client can stream forever
		// without either engaging. Passing w lets net/http close the connection
		// rather than trying to drain the rest.
		r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)

		next(ctx)
	}
}

type modelOutput struct {
	Status int
	Body   Model
}

type modelsOutput struct {
	Body []Model
}

func registerList(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "list-models",
		Summary:     "List models",
		Description: "Every model in the caller's library, newest first.",
		Method:      http.MethodGet,
		Path:        "/api/models",
		Tags:        []string{"library"},
		Errors:      []int{http.StatusUnauthorized},
		Security:    apisec.User(api),
	}, func(ctx context.Context, _ *struct{}) (*modelsOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		models, err := svc.List(ctx, userID)
		if err != nil {
			return nil, err
		}
		return &modelsOutput{Body: models}, nil
	})
}

func registerGet(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "get-model",
		Summary:     "Get a model",
		Description: "One model and the files it owns.",
		Method:      http.MethodGet,
		Path:        "/api/models/{id}",
		Tags:        []string{"library"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
		Security:    apisec.User(api),
	}, func(ctx context.Context, in *struct {
		ID int64 `path:"id"`
	}) (*modelOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		model, err := svc.Get(ctx, userID, in.ID)
		if errors.Is(err, ErrNotFound) {
			// 404 rather than 403: a 403 would confirm that somebody else's
			// model exists at this id.
			return nil, huma.Error404NotFound("model not found")
		}
		if err != nil {
			return nil, err
		}
		return &modelOutput{Status: http.StatusOK, Body: model}, nil
	})
}
