package api

import (
	"net/http"
	"reflect"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"voicebook/internal/core"
	dbx "voicebook/internal/db"
	"voicebook/internal/model"
)

var gorm_session = gorm.Session{}

func setField(v any, name string, val any) {
	rv := reflect.ValueOf(v).Elem()
	f := rv.FieldByName(name)
	if f.IsValid() && f.CanSet() {
		f.Set(reflect.ValueOf(val))
	}
}

func getField(v any, name string) any {
	rv := reflect.ValueOf(v).Elem()
	f := rv.FieldByName(name)
	if !f.IsValid() {
		return nil
	}
	return f.Interface()
}

// crudStudio mounts list/create/update/delete for a studio-owned table.
func crudStudio[T any](s *Server, r chi.Router, path string, managerOnly bool) {
	guard := func(req *http.Request) (*core.Access, error) {
		x, err := s.studio(req)
		if err != nil {
			return nil, err
		}
		if managerOnly && !x.IsManager() {
			return nil, core.Forbidden("只有所有者或管理员可以修改")
		}
		return x, nil
	}
	r.Get(path, h(func(req *http.Request) (any, error) {
		x, err := s.studio(req)
		if err != nil {
			return nil, err
		}
		var list []T
		s.app.DB.Where("studio_id = ?", x.StudioID).Order("created_at asc").Find(&list)
		if list == nil {
			list = []T{}
		}
		return list, nil
	}))
	r.Post(path, h(func(req *http.Request) (any, error) {
		x, err := guard(req)
		if err != nil {
			return nil, err
		}
		var item T
		if err := bind(req, &item); err != nil {
			return nil, err
		}
		setField(&item, "ID", "")
		setField(&item, "StudioID", x.StudioID)
		if _, ok := any(&item).(*model.EffectPreset); ok {
			setField(&item, "Scope", model.ScopeStudio)
		}
		return item, s.app.DB.Create(&item).Error
	}))
	r.Put(path+"/{id}", h(func(req *http.Request) (any, error) {
		x, err := guard(req)
		if err != nil {
			return nil, err
		}
		var item T
		if err := s.app.DB.First(&item, "id = ? AND studio_id = ?", param(req, "id"), x.StudioID).Error; err != nil {
			return nil, core.NotFound("记录")
		}
		id := getField(&item, "ID")
		if err := bind(req, &item); err != nil {
			return nil, err
		}
		setField(&item, "ID", id)
		setField(&item, "StudioID", x.StudioID)
		return item, s.app.DB.Save(&item).Error
	}))
	r.Delete(path+"/{id}", h(func(req *http.Request) (any, error) {
		x, err := guard(req)
		if err != nil {
			return nil, err
		}
		var item T
		return nil, s.app.DB.Where("id = ? AND studio_id = ?", param(req, "id"), x.StudioID).Delete(&item).Error
	}))
}

type adminCrudOpts struct {
	Roles   []string          // admin roles allowed (super always allowed)
	Filters map[string]string // query param -> column
	Search  []string          // columns for ?q=
	Order   string
	Audit   string
	Where   func(q *gorm.DB) *gorm.DB
	Before  func(item any) error
	// BeforeDelete can refuse a deletion (e.g. the record is still in use).
	BeforeDelete func(id string) error
}

func snake(s string) string {
	var out []rune
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				out = append(out, '_')
			}
			out = append(out, unicode.ToLower(r))
		} else {
			out = append(out, r)
		}
	}
	return strings.ReplaceAll(string(out), "_i_d", "_id")
}

// crudAdmin mounts paginated CRUD endpoints for the admin console.
func crudAdmin[T any](s *Server, r chi.Router, path string, o adminCrudOpts) {
	allowed := func(req *http.Request) error {
		if len(o.Roles) == 0 || userOf(req).HasAdminRole(o.Roles...) {
			return nil
		}
		return core.Forbidden("你的管理员角色无权访问此功能")
	}
	r.Get(path, h(func(req *http.Request) (any, error) {
		if err := allowed(req); err != nil {
			return nil, err
		}
		var zero T
		q := s.app.DB.Model(&zero)
		if o.Where != nil {
			q = o.Where(q)
		}
		for param, col := range o.Filters {
			if v := req.URL.Query().Get(param); v != "" {
				q = q.Where(col+" = ?", v)
			}
		}
		if kw := strings.TrimSpace(req.URL.Query().Get("q")); kw != "" && len(o.Search) > 0 {
			var conds []string
			var args []any
			for _, c := range o.Search {
				conds = append(conds, dbx.Like(q, c))
				args = append(args, "%"+kw+"%")
			}
			q = q.Where("("+strings.Join(conds, " OR ")+")", args...)
		}
		return paginate[T](req, q, o.Order)
	}))
	r.Get(path+"/{id}", h(func(req *http.Request) (any, error) {
		if err := allowed(req); err != nil {
			return nil, err
		}
		var item T
		if err := s.app.DB.First(&item, "id = ?", param(req, "id")).Error; err != nil {
			return nil, core.NotFound("记录")
		}
		return item, nil
	}))
	r.Post(path, h(func(req *http.Request) (any, error) {
		if err := allowed(req); err != nil {
			return nil, err
		}
		var item T
		if err := bind(req, &item); err != nil {
			return nil, err
		}
		setField(&item, "ID", "")
		if o.Before != nil {
			if err := o.Before(&item); err != nil {
				return nil, err
			}
		}
		if err := s.app.DB.Create(&item).Error; err != nil {
			return nil, err
		}
		if o.Audit != "" {
			s.app.Audit(userOf(req).ID, o.Audit+".create", o.Audit, toString(getField(&item, "ID")), "", clientIP(req), nil)
		}
		return item, nil
	}))
	r.Put(path+"/{id}", h(func(req *http.Request) (any, error) {
		if err := allowed(req); err != nil {
			return nil, err
		}
		var item T
		if err := s.app.DB.First(&item, "id = ?", param(req, "id")).Error; err != nil {
			return nil, core.NotFound("记录")
		}
		id := getField(&item, "ID")
		if err := bind(req, &item); err != nil {
			return nil, err
		}
		setField(&item, "ID", id)
		if o.Before != nil {
			if err := o.Before(&item); err != nil {
				return nil, err
			}
		}
		if err := s.app.DB.Save(&item).Error; err != nil {
			return nil, err
		}
		if o.Audit != "" {
			s.app.Audit(userOf(req).ID, o.Audit+".update", o.Audit, toString(id), "", clientIP(req), nil)
		}
		return item, nil
	}))
	r.Delete(path+"/{id}", h(func(req *http.Request) (any, error) {
		if err := allowed(req); err != nil {
			return nil, err
		}
		if o.BeforeDelete != nil {
			if err := o.BeforeDelete(param(req, "id")); err != nil {
				return nil, err
			}
		}
		var item T
		if err := s.app.DB.Where("id = ?", param(req, "id")).Delete(&item).Error; err != nil {
			return nil, err
		}
		if o.Audit != "" {
			s.app.Audit(userOf(req).ID, o.Audit+".delete", o.Audit, param(req, "id"), "", clientIP(req), nil)
		}
		return nil, nil
	}))
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func gormExpr(expr string, args ...any) any { return gorm.Expr(expr, args...) }
