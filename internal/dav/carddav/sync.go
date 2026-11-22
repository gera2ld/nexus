package carddav

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"strings"

	webdav "nexus/internal/dav"
	"nexus/internal/dav/internal"
)

// Nexus additions: sync-collection (RFC 6578) and address book PROPPATCH.

// SyncBackend is optionally implemented by a Backend to support the
// sync-collection REPORT. Return webdav.ErrInvalidSyncToken for an unknown
// or expired token.
type SyncBackend interface {
	SyncAddressBook(ctx context.Context, path string, query *SyncQuery) (*SyncResponse, error)
}

// AddressBookUpdate holds PROPPATCH changes; nil fields are untouched and a
// pointer to "" clears the property.
type AddressBookUpdate struct {
	Name        *string
	Description *string
}

// UpdateBackend is optionally implemented by a Backend to allow clients to
// rename or re-describe an address book.
type UpdateBackend interface {
	UpdateAddressBook(ctx context.Context, path string, update *AddressBookUpdate) error
}

func (h *Handler) handleSyncCollection(r *http.Request, w http.ResponseWriter, sc *internal.SyncCollectionQuery) error {
	sb, ok := h.Backend.(SyncBackend)
	if !ok {
		return internal.HTTPErrorf(http.StatusNotImplemented, "carddav: sync-collection not supported")
	}
	b := backend{Backend: h.Backend, Prefix: strings.TrimSuffix(h.Prefix, "/")}
	if b.resourceTypeAtPath(r.URL.Path) != resourceTypeAddressBook {
		return internal.HTTPErrorf(http.StatusForbidden, "carddav: sync-collection is only supported on address books")
	}
	if sc.SyncLevel != "1" && sc.SyncLevel != "infinite" {
		return internal.HTTPErrorf(http.StatusBadRequest, "carddav: invalid sync-level %q", sc.SyncLevel)
	}

	q := SyncQuery{SyncToken: sc.SyncToken}
	if sc.Limit != nil {
		q.Limit = int(sc.Limit.NResults)
	}
	if sc.Prop != nil {
		var addressData addressDataReq
		if err := sc.Prop.Decode(&addressData); err != nil && !internal.IsNotFound(err) {
			return err
		}
		decoded, err := decodeAddressDataReq(&addressData)
		if err != nil {
			return err
		}
		q.DataRequest = *decoded
	}

	res, err := sb.SyncAddressBook(r.Context(), r.URL.Path, &q)
	if errors.Is(err, webdav.ErrInvalidSyncToken) {
		return internal.NewValidSyncTokenError()
	} else if err != nil {
		return err
	}

	propfind := internal.PropFind{Prop: sc.Prop}
	var resps []internal.Response
	for i := range res.Updated {
		resp, err := b.propFindAddressObject(r.Context(), &propfind, &res.Updated[i])
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

func (b *backend) propPatchAddressBook(r *http.Request, update *internal.PropertyUpdate) (*internal.Response, error) {
	ub, ok := b.Backend.(UpdateBackend)
	if !ok {
		return nil, internal.HTTPErrorf(http.StatusForbidden, "carddav: address book properties are read-only")
	}

	var u AddressBookUpdate
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
		addressBookDescriptionName: text(&u.Description, func(raw *internal.RawXMLValue) (string, error) {
			var v addressbookDescription
			err := raw.Decode(&v)
			return v.Description, err
		}),
	}

	resp, ok, err := internal.ApplyPropPatch(r.URL.Path, update, setters)
	if err != nil || !ok {
		return resp, err
	}
	if err := ub.UpdateAddressBook(r.Context(), r.URL.Path, &u); err != nil {
		return nil, err
	}
	return resp, nil
}

// addressBookCollectionProps adds the sync-related properties to an address
// book's PROPFIND response.
func (b *backend) addressBookCollectionProps(props map[xml.Name]internal.PropFindFunc, ab *AddressBook) {
	reports := []xml.Name{addressBookQueryName, addressBookMultigetName}
	if _, ok := b.Backend.(SyncBackend); ok {
		reports = append(reports, internal.SyncCollectionName)
	}
	props[internal.SupportedReportSetName] = internal.PropFindValue(internal.NewSupportedReportSet(reports...))

	if ab.SyncToken != "" {
		props[internal.SyncTokenName] = internal.PropFindValue(&internal.SyncToken{Token: ab.SyncToken})
		props[internal.GetCTagName] = internal.PropFindValue(&internal.GetCTag{CTag: ab.SyncToken})
	}
}
