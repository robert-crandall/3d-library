package library

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/robert-crandall/go-home-server/apisec"
)

type categoriesOutput struct {
	Body []CategorySummary `json:"body" nullable:"false"`
}

type categoryOutput struct {
	Body CategorySummary `json:"body"`
}

type labelsOutput struct {
	Body []LabelSummary `json:"body" nullable:"false"`
}

type labelOutput struct {
	Body LabelSummary `json:"body"`
}

type countsOutput struct {
	Body Counts `json:"body"`
}

// registerTaxonomy mounts the category, tag and material routes plus the
// sidebar's counts.
func registerTaxonomy(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	registerCounts(api, svc, currentUser)
	registerCategories(api, svc, currentUser)
	registerTags(api, svc, currentUser)
	registerMaterials(api, svc, currentUser)
}

func registerCounts(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "get-library-counts",
		Summary:     "Library counts",
		Description: "How many models the caller has, and how many of those " +
			"have no category. Both are over root models; a version never " +
			"appears in the grid, so it is not counted in it either.",
		Method:   http.MethodGet,
		Path:     "/api/library/counts",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized},
		Security: apisec.User(api),
	}, func(ctx context.Context, _ *struct{}) (*countsOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		counts, err := svc.Counts(ctx, userID)
		if err != nil {
			return nil, internalError("could not read the library", err)
		}
		return &countsOutput{Body: counts}, nil
	})
}

func registerCategories(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "list-categories",
		Summary:     "List categories",
		Description: "The caller's categories, by name, each with the number " +
			"of models in it.",
		Method:   http.MethodGet,
		Path:     "/api/categories",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized},
		Security: apisec.User(api),
	}, func(ctx context.Context, _ *struct{}) (*categoriesOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		out, err := svc.ListCategories(ctx, userID)
		if err != nil {
			return nil, internalError("could not read the categories", err)
		}
		return &categoriesOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-category",
		Summary:     "Create a category",
		Description: "Names are unique per user, ignoring case. A duplicate is " +
			"a 422 and creates nothing.",
		Method:        http.MethodPost,
		Path:          "/api/categories",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusUnprocessableEntity},
		Security:      apisec.User(api),
	}, func(ctx context.Context, in *struct {
		Body struct {
			Name  string `json:"name" required:"true" maxLength:"60" doc:"What to call it"`
			Color string `json:"color" required:"true" pattern:"^#[0-9a-fA-F]{6}$" doc:"A #rrggbb colour"`
		}
	}) (*categoryOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		c, err := svc.CreateCategory(ctx, userID, in.Body.Name, in.Body.Color)
		if err != nil {
			return nil, taxonomyError("could not save the category", "category", err)
		}
		return &categoryOutput{Body: c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-category",
		Summary:     "Rename a category",
		Description: "Replaces the name and the colour. Renaming onto an " +
			"existing name is a duplicate, not a merge.",
		Method:   http.MethodPut,
		Path:     "/api/categories/{id}",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity},
		Security: apisec.User(api),
	}, func(ctx context.Context, in *struct {
		ID   int64 `path:"id"`
		Body struct {
			Name  string `json:"name" required:"true" maxLength:"60" doc:"What to call it"`
			Color string `json:"color" required:"true" pattern:"^#[0-9a-fA-F]{6}$" doc:"A #rrggbb colour"`
		}
	}) (*categoryOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		c, err := svc.UpdateCategory(ctx, userID, in.ID, in.Body.Name, in.Body.Color)
		if errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("category not found")
		}
		if err != nil {
			return nil, taxonomyError("could not save the category", "category", err)
		}
		return &categoryOutput{Body: c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-category",
		Summary:     "Delete a category",
		Description: "The models in it are kept and become uncategorized. " +
			"There is no undo.",
		Method:        http.MethodDelete,
		Path:          "/api/categories/{id}",
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
		if err := svc.DeleteCategory(ctx, userID, in.ID); errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("category not found")
		} else if err != nil {
			return nil, internalError("could not delete the category", err)
		}
		return nil, nil
	})
}

func registerTags(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "list-tags",
		Summary:     "List tags",
		Description: "The caller's tags, by name, each with the number of " +
			"models carrying it.",
		Method:   http.MethodGet,
		Path:     "/api/tags",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized},
		Security: apisec.User(api),
	}, func(ctx context.Context, _ *struct{}) (*labelsOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		out, err := svc.ListTags(ctx, userID)
		if err != nil {
			return nil, internalError("could not read the tags", err)
		}
		return &labelsOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-tag",
		Summary:     "Create a tag",
		Description: "Names are unique per user, ignoring case. A duplicate is " +
			"a 422 and creates nothing.",
		Method:        http.MethodPost,
		Path:          "/api/tags",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusUnprocessableEntity},
		Security:      apisec.User(api),
	}, func(ctx context.Context, in *nameBody) (*labelOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		l, err := svc.CreateTag(ctx, userID, in.Body.Name)
		if err != nil {
			return nil, taxonomyError("could not save the tag", "tag", err)
		}
		return &labelOutput{Body: l}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-tag",
		Summary:     "Rename a tag",
		Description: "Renaming onto an existing name is a duplicate, not a merge.",
		Method:      http.MethodPut,
		Path:        "/api/tags/{id}",
		Tags:        []string{"library"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity},
		Security:    apisec.User(api),
	}, func(ctx context.Context, in *namedBody) (*labelOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		l, err := svc.UpdateTag(ctx, userID, in.ID, in.Body.Name)
		if errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("tag not found")
		}
		if err != nil {
			return nil, taxonomyError("could not save the tag", "tag", err)
		}
		return &labelOutput{Body: l}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-tag",
		Summary:       "Delete a tag",
		Description:   "The models carrying it keep everything else. There is no undo.",
		Method:        http.MethodDelete,
		Path:          "/api/tags/{id}",
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
		if err := svc.DeleteTag(ctx, userID, in.ID); errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("tag not found")
		} else if err != nil {
			return nil, internalError("could not delete the tag", err)
		}
		return nil, nil
	})
}

func registerMaterials(api huma.API, svc *Service, currentUser CurrentUserFunc) {
	huma.Register(api, huma.Operation{
		OperationID: "list-materials",
		Summary:     "List materials",
		Description: "The caller's materials, by name, each with the number of " +
			"models using it. Every account starts with the five common " +
			"filaments; they are ordinary rows and may be renamed or deleted.",
		Method:   http.MethodGet,
		Path:     "/api/materials",
		Tags:     []string{"library"},
		Errors:   []int{http.StatusUnauthorized},
		Security: apisec.User(api),
	}, func(ctx context.Context, _ *struct{}) (*labelsOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		out, err := svc.ListMaterials(ctx, userID)
		if err != nil {
			return nil, internalError("could not read the materials", err)
		}
		return &labelsOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-material",
		Summary:     "Create a material",
		Description: "Names are unique per user, ignoring case. A duplicate is " +
			"a 422 and creates nothing.",
		Method:        http.MethodPost,
		Path:          "/api/materials",
		Tags:          []string{"library"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusUnprocessableEntity},
		Security:      apisec.User(api),
	}, func(ctx context.Context, in *nameBody) (*labelOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		l, err := svc.CreateMaterial(ctx, userID, in.Body.Name)
		if err != nil {
			return nil, taxonomyError("could not save the material", "material", err)
		}
		return &labelOutput{Body: l}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-material",
		Summary:     "Rename a material",
		Description: "Renaming onto an existing name is a duplicate, not a merge.",
		Method:      http.MethodPut,
		Path:        "/api/materials/{id}",
		Tags:        []string{"library"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity},
		Security:    apisec.User(api),
	}, func(ctx context.Context, in *namedBody) (*labelOutput, error) {
		userID, err := currentUser(ctx)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		l, err := svc.UpdateMaterial(ctx, userID, in.ID, in.Body.Name)
		if errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("material not found")
		}
		if err != nil {
			return nil, taxonomyError("could not save the material", "material", err)
		}
		return &labelOutput{Body: l}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-material",
		Summary:     "Delete a material",
		Description: "The models using it keep everything else. There is no " +
			"undo, and a deleted seeded material does not come back.",
		Method:        http.MethodDelete,
		Path:          "/api/materials/{id}",
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
		if err := svc.DeleteMaterial(ctx, userID, in.ID); errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("material not found")
		} else if err != nil {
			return nil, internalError("could not delete the material", err)
		}
		return nil, nil
	})
}

// nameBody and namedBody are the create and rename shapes tags and materials
// share. They are named types rather than anonymous structs because huma names
// the generated schema after the Go type, and two anonymous structs with the
// same fields would otherwise collide in the spec.
type nameBody struct {
	Body struct {
		Name string `json:"name" required:"true" maxLength:"60" doc:"What to call it"`
	}
}

type namedBody struct {
	ID   int64 `path:"id"`
	Body struct {
		Name string `json:"name" required:"true" maxLength:"60" doc:"What to call it"`
	}
}

// taxonomyError maps a create or rename failure onto a status.
//
// A duplicate gets its own sentence rather than the wrapped error's, because
// the wrapped one names the table and the constraint: huma renders err.Error()
// into errors[].message and the frontend shows that field to the user. Same
// rule as internalError, applied one case earlier.
func taxonomyError(message, what string, err error) error {
	switch {
	case errors.Is(err, ErrDuplicate):
		return huma.Error422UnprocessableEntity("that " + what + " already exists")
	case errors.Is(err, errInvalid):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		return internalError(message, err)
	}
}
