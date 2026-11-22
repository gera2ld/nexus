package caldav

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"strings"

	webdav "nexus/internal/dav"
	"nexus/internal/dav/internal"
)

// Nexus additions: sync-collection (RFC 6578) and calendar PROPPATCH.

// SyncBackend is optionally implemented by a Backend to support the
// sync-collection REPORT. Return webdav.ErrInvalidSyncToken for an unknown
// or expired token.
type SyncBackend interface {
	SyncCalendar(ctx context.Context, path string, query *SyncQuery) (*SyncResponse, error)
}

// CalendarUpdate holds PROPPATCH changes; nil fields are untouched and a
// pointer to "" clears the property.
type CalendarUpdate struct {
	Name        *string
	Description *string
	Color       *string
}

// UpdateBackend is optionally implemented by a Backend to allow clients to
// rename, recolour or re-describe a calendar.
type UpdateBackend interface {
	UpdateCalendar(ctx context.Context, path string, update *CalendarUpdate) error
}

func (h *Handler) handleSyncCollection(r *http.Request, w http.ResponseWriter, sc *internal.SyncCollectionQuery) error {
	sb, ok := h.Backend.(SyncBackend)
	if !ok {
		return internal.HTTPErrorf(http.StatusNotImplemented, "caldav: sync-collection not supported")
	}
	b := backend{Backend: h.Backend, Prefix: strings.TrimSuffix(h.Prefix, "/")}
	if b.resourceTypeAtPath(r.URL.Path) != resourceTypeCalendar {
		return internal.HTTPErrorf(http.StatusForbidden, "caldav: sync-collection is only supported on calendars")
	}
	if sc.SyncLevel != "1" && sc.SyncLevel != "infinite" {
		return internal.HTTPErrorf(http.StatusBadRequest, "caldav: invalid sync-level %q", sc.SyncLevel)
	}

	q := SyncQuery{SyncToken: sc.SyncToken}
	if sc.Limit != nil {
		q.Limit = int(sc.Limit.NResults)
	}
	if sc.Prop != nil {
		var calendarData calendarDataReq
		if err := sc.Prop.Decode(&calendarData); err != nil && !internal.IsNotFound(err) {
			return err
		}
		decoded, err := decodeCalendarDataReq(&calendarData)
		if err != nil {
			return err
		}
		q.CompRequest = *decoded
	}

	res, err := sb.SyncCalendar(r.Context(), r.URL.Path, &q)
	if errors.Is(err, webdav.ErrInvalidSyncToken) {
		return internal.NewValidSyncTokenError()
	} else if err != nil {
		return err
	}

	propfind := internal.PropFind{Prop: sc.Prop}
	var resps []internal.Response
	for i := range res.Updated {
		resp, err := b.propFindCalendarObject(r.Context(), &propfind, &res.Updated[i])
		if err != nil {
			return err
		}
		resps = append(resps, *resp)
	}
	for _, p := range res.Deleted {
		resps = append(resps, internal.Response{
			Hrefs:  []internal.Href{{Path: p}},
			Status: &internal.Status{Code: http.StatusNotFound},
		})
	}
	if res.Truncated {
		resps = append(resps, internal.Response{
			Hrefs:  []internal.Href{{Path: r.URL.Path}},
			Status: &internal.Status{Code: http.StatusInsufficientStorage},
		})
	}

	ms := internal.NewMultiStatus(resps...)
	ms.SyncToken = res.SyncToken
	return internal.ServeMultiStatus(w, ms)
}

func (b *backend) propPatchCalendar(r *http.Request, update *internal.PropertyUpdate) (*internal.Response, error) {
	ub, ok := b.Backend.(UpdateBackend)
	if !ok {
		return nil, internal.HTTPErrorf(http.StatusForbidden, "caldav: calendar properties are read-only")
	}

	var u CalendarUpdate
	text := func(dst **string, decode func(*internal.RawXMLValue) (string, error)) internal.PropPatchSetter {
		return func(raw *internal.RawXMLValue) error {
			s := ""
			if raw != nil {
				var err error
				if s, err = decode(raw); err != nil {
					return err
				}
			}
			*dst = &s
			return nil
		}
	}
	setters := map[xml.Name]internal.PropPatchSetter{
		internal.DisplayNameName: text(&u.Name, func(raw *internal.RawXMLValue) (string, error) {
			var v internal.DisplayName
			err := raw.Decode(&v)
			return v.Name, err
		}),
		calendarDescriptionName: text(&u.Description, func(raw *internal.RawXMLValue) (string, error) {
			var v calendarDescription
			err := raw.Decode(&v)
			return v.Description, err
		}),
		calendarColorName: text(&u.Color, func(raw *internal.RawXMLValue) (string, error) {
			var v calendarColor
			err := raw.Decode(&v)
			return v.Color, err
		}),
	}

	resp, ok, err := internal.ApplyPropPatch(r.URL.Path, update, setters)
	if err != nil || !ok {
		return resp, err
	}
	if err := ub.UpdateCalendar(r.Context(), r.URL.Path, &u); err != nil {
		return nil, err
	}
	return resp, nil
}

// calendarCollectionProps adds the sync-related properties to a calendar's
// PROPFIND response.
func (b *backend) calendarCollectionProps(props map[xml.Name]internal.PropFindFunc, cal *Calendar) {
	reports := []xml.Name{calendarQueryName, calendarMultigetName}
	if _, ok := b.Backend.(SyncBackend); ok {
		reports = append(reports, internal.SyncCollectionName)
	}
	props[internal.SupportedReportSetName] = internal.PropFindValue(internal.NewSupportedReportSet(reports...))

	if cal.SyncToken != "" {
		props[internal.SyncTokenName] = internal.PropFindValue(&internal.SyncToken{Token: cal.SyncToken})
		props[internal.GetCTagName] = internal.PropFindValue(&internal.GetCTag{CTag: cal.SyncToken})
	}
}
