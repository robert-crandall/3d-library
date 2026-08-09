package library

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/robert-crandall/go-home-server/apisec"
)

type collectionsOutput struct {
	Body []CollectionSummary `json:"body" nullable:"false"`
}

type collectionOutput struct {
	Body CollectionSummary `json:"body"`
}

// collectionBody is the create and rename shape. A named type rather than an
// anonymous struct for the reason nameBody is one: huma names the generated
// schema after the Go type, and two anonymous structs with the same fields
// collide in the spec.
type collectionBody struct {
	Name        string `json:"name" required:"true" maxLength:"60" doc:"What to call it"`
	Description string `json:"description" maxLength:"500" doc:"An optional note about the project"`
}

// membershipInput addresses one model's membership of one collection. Both ids
// are in the path because the membership is the resource, which is what lets
// adding be a PUT that can be repeated.
type membershipInput struct {
	ID           int64 `path:"id"`
	CollectionID int64 `path:"collectionId"`
}

// registerCollections mounts the collection routes and the two that move a
// model in and out of one.
func registerCollections(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "list-collections",
		Summary:     "List collections",
		Description: "The caller's collections, by name, each with the number " +
			"of models in it. Over root models, like every count here.",
		Method:   http.MethodGet,
		Path:     "/api/collections",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized},
		Security: apisec.User(api),
	}, func(ctx context.Context, _ *struct{}) (*collectionsOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		out, err := svc.ListCollections(ctx, userID)
		if err != nil {
			return nil, internalError("could not read the collections", err)
		}
		return &collectionsOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-collection",
		Summary:     "Create a collection",
		Description: "Names are unique per user, ignoring case. A duplicate is " +
			"a 422 and creates nothing. The description is optional.",
		Method:        http.MethodPost,
		Path:          "/api/collections",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusUnprocessableEntity},
		Security:      apisec.User(api),
	}, func(ctx context.Context, in *struct {
		Body collectionBody
	}) (*collectionOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		c, err := svc.CreateCollection(ctx, userID, in.Body.Name, in.Body.Description)
		if err != nil {
			return nil, taxonomyError("could not save the collection", "collection", err)
		}
		return &collectionOutput{Body: c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-collection",
		Summary:     "Rename a collection",
		Description: "Replaces the name and the description. Renaming onto an " +
			"existing name is a duplicate, not a merge.",
		Method:   http.MethodPut,
		Path:     "/api/collections/{id}",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity},
		Security: apisec.User(api),
	}, func(ctx context.Context, in *struct {
		ID   int64 `path:"id"`
		Body collectionBody
	}) (*collectionOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		c, err := svc.UpdateCollection(ctx, userID, in.ID, in.Body.Name, in.Body.Description)
		if errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("collection not found")
		}
		if err != nil {
			return nil, taxonomyError("could not save the collection", "collection", err)
		}
		return &collectionOutput{Body: c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-collection",
		Summary:     "Delete a collection",
		Description: "The models in it are kept and stay in the library. A " +
			"collection is a bag, not a container. There is no undo.",
		Method:        http.MethodDelete,
		Path:          "/api/collections/{id}",
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
		if err := svc.DeleteCollection(ctx, userID, in.ID); errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("collection not found")
		} else if err != nil {
			return nil, internalError("could not delete the collection", err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "add-model-to-collection",
		Summary:     "Add a model to a collection",
		Description: "Repeatable: a model that is already in the collection " +
			"stays in it once and this still succeeds. Either id not being " +
			"the caller's is a 404.",
		Method:        http.MethodPut,
		Path:          "/api/models/{id}/collections/{collectionId}",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusNotFound},
		Security:      apisec.User(api),
	}, func(ctx context.Context, in *membershipInput) (*struct{}, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		if err := svc.AddModelToCollection(ctx, userID, in.ID, in.CollectionID); errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("model or collection not found")
		} else if err != nil {
			return nil, internalError("could not add the model to the collection", err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "remove-model-from-collection",
		Summary:     "Remove a model from a collection",
		Description: "The model stays in the library with its files and its " +
			"tags. Removing one that is not in the collection is a 404.",
		Method:        http.MethodDelete,
		Path:          "/api/models/{id}/collections/{collectionId}",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusNotFound},
		Security:      apisec.User(api),
	}, func(ctx context.Context, in *membershipInput) (*struct{}, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		if err := svc.RemoveModelFromCollection(ctx, userID, in.ID, in.CollectionID); errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("model or collection not found")
		} else if err != nil {
			return nil, internalError("could not remove the model from the collection", err)
		}
		return nil, nil
	})
}
