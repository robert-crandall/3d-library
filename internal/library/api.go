package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	registerUpdate(api, svc, currentUser)
	registerDeleteModel(api, svc, currentUser)
	registerDeleteFile(api, svc, currentUser)
	registerDownload(api, svc, currentUser)
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

	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
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
		Method: http.MethodPost,
		Path:   "/api/models",
		Tags:   []string{"library"},
		// Declared, not just returned. Setting it only on the output struct
		// leaves the published spec saying 200 while the server answers 201,
		// and a generated client believes the spec.
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity},
		Security:      apisec.User(api),
		Middlewares:   huma.Middlewares{svc.guardUpload(api, currentUser)},

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
		return &modelOutput{Body: model}, nil
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
	Body File
}

func registerAddFile(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "add-model-file",
		Summary:     "Add a file to a model",
		Description: "Uploads one more file into an existing model. One request " +
			"carries one file.",
		Method:        http.MethodPost,
		Path:          "/api/models/{id}/files",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity},
		Security:      apisec.User(api),
		Middlewares:   huma.Middlewares{svc.guardUpload(api, currentUser)},

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
		return &fileOutput{Body: file}, nil
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
		// The two cases above are the app's own sentences, written to be read.
		// Anything else is wrapped from pgx or the disk, so it goes to the log
		// and the caller gets a sentence. Same rule as internalError below.
		return internalError("could not save the model", err)
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
	Body ModelDetail
}

type modelsOutput struct {
	// nullable:"false" for the same reason ModelDetail.Files carries it: huma
	// types a Go slice as ["array","null"], the handler always returns a slice,
	// and a contract that says null is possible makes every caller carry a
	// branch for something that cannot arrive.
	Body []Model `json:"body" nullable:"false"`
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
			return nil, internalError("could not read the library", err)
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
			return nil, internalError("could not read the model", err)
		}
		return &modelOutput{Body: model}, nil
	})
}

// updateInput is the whole editable metadata surface, every field required.
//
// This is PUT, not PATCH, because it is a full replacement: the detail screen
// submits one form containing all four fields, so there is no partial update to
// express. Pointer fields and a "which keys arrived" branch would buy partial
// semantics for a caller that does not exist.
type updateInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Name        string `json:"name" required:"true" maxLength:"200" doc:"The model's display name"`
		Description string `json:"description" required:"true" maxLength:"5000" doc:"Free text, may be empty"`
		PrintTips   string `json:"printTips" required:"true" maxLength:"5000" doc:"One tip per line, may be empty"`
		SourceURL   string `json:"sourceUrl" required:"true" maxLength:"2000" doc:"An http:// or https:// address, or empty"`
	}
}

func registerUpdate(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "update-model",
		Summary:     "Update a model's metadata",
		Description: "Replaces the model's editable metadata. All four fields " +
			"are sent every time; an empty string clears a field. The name may " +
			"not be blank.",
		Method:   http.MethodPut,
		Path:     "/api/models/{id}",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity},
		Security: apisec.User(api),
	}, func(ctx context.Context, in *updateInput) (*modelOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		model, err := svc.Update(ctx, userID, in.ID, Edits{
			Name:        in.Body.Name,
			Description: in.Body.Description,
			PrintTips:   in.Body.PrintTips,
			SourceURL:   in.Body.SourceURL,
		})
		if errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("model not found")
		}
		if err != nil {
			return nil, uploadError(err)
		}
		return &modelOutput{Body: model}, nil
	})
}

func registerDeleteModel(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID:   "delete-model",
		Summary:       "Delete a model",
		Description:   "Removes the model, every file it owns, and their stored blobs. There is no undo.",
		Method:        http.MethodDelete,
		Path:          "/api/models/{id}",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusNotFound},
		Security:      apisec.User(api),
	}, func(ctx context.Context, in *struct {
		ID int64 `path:"id"`
	}) (*struct{}, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		if err := svc.DeleteModel(ctx, userID, in.ID); errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("model not found")
		} else if err != nil {
			return nil, internalError("could not delete the model", err)
		}
		return nil, nil
	})
}

func registerDeleteFile(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-model-file",
		Summary:     "Delete a file from a model",
		Description: "Removes one file and its stored blob. The model stays, " +
			"even if that was its last file.",
		Method:        http.MethodDelete,
		Path:          "/api/models/{id}/files/{fileId}",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusNotFound},
		Security:      apisec.User(api),
	}, func(ctx context.Context, in *struct {
		ID     int64 `path:"id"`
		FileID int64 `path:"fileId"`
	}) (*struct{}, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		if err := svc.DeleteFile(ctx, userID, in.ID, in.FileID); errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("file not found")
		} else if err != nil {
			return nil, internalError("could not delete the file", err)
		}
		return nil, nil
	})
}

func registerDownload(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "download-model-file",
		Summary:     "Download a file's contents",
		Description: "The file's raw bytes, always as an attachment.",
		Method:      http.MethodGet,
		Path:        "/api/models/{id}/files/{fileId}",
		Tags:        []string{"library"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
		Security:    apisec.User(api),

		// huma cannot infer a body schema from StreamResponse, so without this
		// the committed spec claims the endpoint returns nothing at all.
		// octet-stream is the standard way to say "arbitrary bytes"; the
		// runtime Content-Type is whatever was sniffed at upload.
		Responses: map[string]*huma.Response{
			"200": {
				Description: "The file's raw bytes. The response Content-Type is the type detected at upload, not necessarily application/octet-stream.",
				Content: map[string]*huma.MediaType{
					"application/octet-stream": {
						Schema: &huma.Schema{Type: "string", Format: "binary"},
					},
				},
			},
		},
	}, func(ctx context.Context, in *struct {
		ID     int64 `path:"id"`
		FileID int64 `path:"fileId"`
	}) (*huma.StreamResponse, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		fh, meta, err := svc.Open(ctx, userID, in.ID, in.FileID)
		if errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("file not found")
		}
		if err != nil {
			return nil, internalError("could not read the file", err)
		}

		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			defer fh.Close()
			r, w := humachi.Unwrap(hctx)
			w.Header().Set("Content-Type", meta.ContentType)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			// Always attachment, never inline. The foundation's file service
			// inlines text and images; this one deliberately does not reuse
			// that. content_type is sniffed from the bytes, so a file whose
			// bytes really are HTML is served text/html - and inline would make
			// that stored XSS on the app's own origin. Nothing in this app
			// needs a model file rendered in a tab.
			w.Header().Set("Content-Disposition",
				mime.FormatMediaType("attachment", map[string]string{"filename": meta.Filename}))
			// Per-user and deletable, so it must not be cached without
			// revalidation: a browser cache keys on URL, not on session.
			w.Header().Set("Cache-Control", "private, no-cache")
			// ServeContent sets Content-Length and answers Range and
			// conditional requests in less code than doing it by hand. huma
			// runs this before writing status or headers, so its 206 and 304
			// responses land intact. The modtime must be non-zero or
			// If-Modified-Since is skipped.
			http.ServeContent(w, r, meta.Filename, meta.CreatedAt, fh)
		}}, nil
	})
}

// internalError is the only way an unexpected failure leaves this package. huma
// renders err.Error() as the problem detail, and these errors are wrapped all
// the way down: a Postgres message, or - from the download path - a storage key
// and the directory it lives in. The caller gets a sentence, the log gets the
// error.
func internalError(message string, err error) error {
	slog.Error(message, "error", err)
	return huma.Error500InternalServerError(message)
}
