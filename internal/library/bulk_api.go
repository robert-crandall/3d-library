package library

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/robert-crandall/go-home-server/apisec"
)

// The bulk routes hang off a static `bulk` segment under /api/models. chi
// resolves a static segment before a parameter one, so /api/models/bulk/tags
// cannot be read as /api/models/{id}/..., and a test asserts both still route.
//
// Four separate write routes rather than one endpoint taking an action name: a
// discriminated body is a union in the spec, and openapi-typescript renders a
// union of these as an intersection of optionals, so the generated client would
// stop guaranteeing that a recategorize carries a category. Each of these has
// one exact body that huma validates from the schema for free.
const bulkPrefix = "/api/models/bulk"

// The selection field is spelled out in each request body rather than shared
// through an embedded struct: huma builds a body's schema from the struct's own
// fields and does not flatten an embedded one, so the shared version compiled,
// generated a schema with no modelIds in it, and - because huma sets
// additionalProperties false - rejected every request that sent one.
//
// uniqueItems is a documented promise rather than the enforcement. The service
// dedupes anyway, because every ownership check there compares a resolved count
// against the length of this list.

type bulkTagsInput struct {
	Body struct {
		ModelIDs []int64 `json:"modelIds" required:"true" nullable:"false" minItems:"1" maxItems:"200" uniqueItems:"true" doc:"The selected models"`
		TagIDs   []int64 `json:"tagIds" required:"true" nullable:"false" minItems:"1" maxItems:"100" uniqueItems:"true" doc:"The tags to add"`
	}
}

type bulkCategoryInput struct {
	Body struct {
		ModelIDs []int64 `json:"modelIds" required:"true" nullable:"false" minItems:"1" maxItems:"200" uniqueItems:"true" doc:"The selected models"`
		// Not a pointer: clearing a category in bulk is out of scope, so there
		// is no null to express and required means present-and-a-number.
		CategoryID int64 `json:"categoryId" required:"true" doc:"The category to put them all in"`
	}
}

type bulkCollectionInput struct {
	Body struct {
		ModelIDs     []int64 `json:"modelIds" required:"true" nullable:"false" minItems:"1" maxItems:"200" uniqueItems:"true" doc:"The selected models"`
		CollectionID int64   `json:"collectionId" required:"true" doc:"The collection to add them to"`
	}
}

type bulkPreviewInput struct {
	Body struct {
		ModelIDs []int64 `json:"modelIds" required:"true" nullable:"false" minItems:"1" maxItems:"200" uniqueItems:"true" doc:"The selected models"`
	}
}

type bulkDeleteInput struct {
	Body struct {
		ModelIDs []int64 `json:"modelIds" required:"true" nullable:"false" minItems:"1" maxItems:"200" uniqueItems:"true" doc:"The selected models"`
		// What the caller was shown. See BulkDelete: the confirmation sentence
		// is the only thing standing between a click and a permanent delete, so
		// the numbers in it are rechecked under the locks.
		ExpectVersions int `json:"expectVersions" required:"true" minimum:"0" doc:"Versions the preview reported"`
		ExpectFiles    int `json:"expectFiles" required:"true" minimum:"0" doc:"Files the preview reported"`
	}
}

type bulkPreviewOutput struct {
	Body DeletePreview
}

// bulkError maps what every bulk action can fail with. A model the caller does
// not own is a 404 and is indistinguishable from one that is not there, which
// is the rule everywhere else here; a tag, category or collection it does not
// own is a 422, because the models were addressable and the request was not.
// Every bulk action can hit lockModels' ErrChanged, not just the delete, so the
// mapping lives here rather than at the one call site that used to raise it.
func bulkError(message string, err error) error {
	switch {
	case errors.Is(err, ErrChanged):
		return huma.Error409Conflict("those models changed while this was being applied; try again")
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound("model not found")
	case errors.Is(err, errInvalid):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		return internalError(message, err)
	}
}

// registerBulk mounts the four bulk actions and the count the delete
// confirmation is built from.
func registerBulk(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "bulk-add-tags",
		Summary:     "Tag several models",
		Description: "Adds every tag to every selected model. Tags already on a " +
			"model are left alone and are not an error. Either all the models " +
			"change or none do.",
		Method:        http.MethodPost,
		Path:          bulkPrefix + "/tags",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusUnauthorized, http.StatusNotFound,
			http.StatusConflict, http.StatusUnprocessableEntity},
		Security: apisec.User(api),
	}, func(ctx context.Context, in *bulkTagsInput) (*struct{}, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		if err := svc.BulkAddTags(ctx, userID, in.Body.ModelIDs, in.Body.TagIDs); err != nil {
			return nil, bulkError("could not tag the models", err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "bulk-set-category",
		Summary:     "Recategorize several models",
		Description: "Puts every selected model in one category, replacing whatever " +
			"each had. Either all the models change or none do.",
		Method:        http.MethodPost,
		Path:          bulkPrefix + "/category",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusUnauthorized, http.StatusNotFound,
			http.StatusConflict, http.StatusUnprocessableEntity},
		Security: apisec.User(api),
	}, func(ctx context.Context, in *bulkCategoryInput) (*struct{}, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		if err := svc.BulkSetCategory(ctx, userID, in.Body.ModelIDs, in.Body.CategoryID); err != nil {
			return nil, bulkError("could not recategorize the models", err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "bulk-add-to-collection",
		Summary:     "Add several models to a collection",
		Description: "Adds every selected model to one collection, skipping the ones " +
			"already in it. Either all the models are added or none are.",
		Method:        http.MethodPost,
		Path:          bulkPrefix + "/collection",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusUnauthorized, http.StatusNotFound,
			http.StatusConflict, http.StatusUnprocessableEntity},
		Security: apisec.User(api),
	}, func(ctx context.Context, in *bulkCollectionInput) (*struct{}, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		if err := svc.BulkAddToCollection(ctx, userID, in.Body.ModelIDs, in.Body.CollectionID); err != nil {
			return nil, bulkError("could not add the models to the collection", err)
		}
		return nil, nil
	})

	// A POST that reads, because the input is a list of up to 200 ids and those
	// do not belong in a query string.
	huma.Register(api, huma.Operation{
		OperationID: "preview-bulk-delete",
		Summary:     "Count what a bulk delete would destroy",
		Description: "The selected models, the versions that will go with them, and " +
			"every file between them. Deletes are permanent, so the confirmation " +
			"says exactly this and the delete rechecks it.",
		Method:   http.MethodPost,
		Path:     bulkPrefix + "/delete-preview",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity},
		Security: apisec.User(api),
	}, func(ctx context.Context, in *bulkPreviewInput) (*bulkPreviewOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		out, err := svc.PreviewBulkDelete(ctx, userID, in.Body.ModelIDs)
		if err != nil {
			return nil, bulkError("could not read the selection", err)
		}
		return &bulkPreviewOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "bulk-delete-models",
		Summary:     "Delete several models",
		Description: "Deletes every selected model, its versions, and all of their " +
			"files. This cannot be undone. expectVersions and expectFiles are the " +
			"numbers the preview reported; if the selection has grown since, the " +
			"delete is refused with a 409 and nothing is destroyed.",
		Method:        http.MethodPost,
		Path:          bulkPrefix + "/delete",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusUnauthorized, http.StatusNotFound,
			http.StatusConflict, http.StatusUnprocessableEntity},
		Security: apisec.User(api),
	}, func(ctx context.Context, in *bulkDeleteInput) (*struct{}, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		expect := DeletePreview{Versions: in.Body.ExpectVersions, Files: in.Body.ExpectFiles}
		err = svc.BulkDelete(ctx, userID, in.Body.ModelIDs, expect)
		if errors.Is(err, ErrChanged) {
			return nil, huma.Error409Conflict("the selection changed; check what will be deleted again")
		}
		if err != nil {
			return nil, bulkError("could not delete the models", err)
		}
		return nil, nil
	})
}
