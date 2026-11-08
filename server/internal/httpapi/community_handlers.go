package httpapi

import (
	"context"
	"errors"

	"github.com/openbekci/openbekci/server/internal/core/community"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Handlers of the community module (facilities and bookings, visitors,
// recurring visitors, approvals, the guard console, parcels). The contract is
// the draft's community module, served as x-stability: preview. The two QR
// image operations stay draft-only until a community.QRRenderer is wired.

var errNoCommunity = errors.New("community service not configured")

func (s *apiServer) cm() (*community.Service, error) {
	if s.community == nil {
		return nil, problem.Internal(errNoCommunity)
	}
	return s.community, nil
}

func guardToken(h *string) string {
	if h == nil {
		return ""
	}
	return *h
}

// --- facilities ---

func (s *apiServer) CommunityListFacilities(ctx context.Context, req gen.CommunityListFacilitiesRequestObject) (gen.CommunityListFacilitiesResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	items, next, err := c.ListFacilities(ctx, req.OrgId, req.Params.SiteId, req.Params.Enabled, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.FacilityList](items, next)
	return gen.CommunityListFacilities200JSONResponse(out), err
}

func (s *apiServer) CommunityCreateFacility(ctx context.Context, req gen.CommunityCreateFacilityRequestObject) (gen.CommunityCreateFacilityResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	d, err := commBodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	f, err := c.CreateFacility(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Facility](f)
	return gen.CommunityCreateFacility201JSONResponse(out), err
}

func (s *apiServer) CommunityGetFacility(ctx context.Context, req gen.CommunityGetFacilityRequestObject) (gen.CommunityGetFacilityResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	f, err := c.GetFacility(ctx, req.OrgId, req.FacilityId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Facility](f)
	return gen.CommunityGetFacility200JSONResponse(out), err
}

func (s *apiServer) CommunityUpdateFacility(ctx context.Context, req gen.CommunityUpdateFacilityRequestObject) (gen.CommunityUpdateFacilityResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	d, err := commBodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	f, err := c.UpdateFacility(ctx, req.OrgId, req.FacilityId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Facility](f)
	return gen.CommunityUpdateFacility200JSONResponse(out), err
}

func (s *apiServer) CommunityDeleteFacility(ctx context.Context, req gen.CommunityDeleteFacilityRequestObject) (gen.CommunityDeleteFacilityResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := c.DeleteFacility(ctx, req.OrgId, req.FacilityId, v); err != nil {
		return nil, err
	}
	return gen.CommunityDeleteFacility204Response{}, nil
}

func (s *apiServer) CommunityGetFacilityAvailability(ctx context.Context, req gen.CommunityGetFacilityAvailabilityRequestObject) (gen.CommunityGetFacilityAvailabilityResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	a, err := c.FacilityAvailability(ctx, req.OrgId, req.FacilityId, req.Params.From, req.Params.To)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Availability](a)
	return gen.CommunityGetFacilityAvailability200JSONResponse(out), err
}

func (s *apiServer) CommunityListFacilityBlocks(ctx context.Context, req gen.CommunityListFacilityBlocksRequestObject) (gen.CommunityListFacilityBlocksResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	items, next, err := c.ListFacilityBlocks(ctx, req.OrgId, req.FacilityId, req.Params.From, req.Params.To, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.FacilityBlockList](items, next)
	return gen.CommunityListFacilityBlocks200JSONResponse(out), err
}

func (s *apiServer) CommunityCreateFacilityBlock(ctx context.Context, req gen.CommunityCreateFacilityBlockRequestObject) (gen.CommunityCreateFacilityBlockResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	d, err := commBodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	b, err := c.CreateFacilityBlock(ctx, req.OrgId, req.FacilityId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.FacilityBlock](b)
	return gen.CommunityCreateFacilityBlock201JSONResponse(out), err
}

func (s *apiServer) CommunityDeleteFacilityBlock(ctx context.Context, req gen.CommunityDeleteFacilityBlockRequestObject) (gen.CommunityDeleteFacilityBlockResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	if err := c.DeleteFacilityBlock(ctx, req.OrgId, req.FacilityId, req.BlockId); err != nil {
		return nil, err
	}
	return gen.CommunityDeleteFacilityBlock204Response{}, nil
}

// --- bookings ---

func (s *apiServer) CommunityListBookings(ctx context.Context, req gen.CommunityListBookingsRequestObject) (gen.CommunityListBookingsResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	f := community.BookingFilter{SiteID: req.Params.SiteId, FacilityID: req.Params.FacilityId, UnitID: req.Params.UnitId, From: req.Params.From, To: req.Params.To}
	if req.Params.Status != nil {
		f.Status = *req.Params.Status
	}
	f.Mine = req.Params.Mine != nil && *req.Params.Mine
	items, next, err := c.ListBookings(ctx, req.OrgId, f, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.BookingList](items, next)
	return gen.CommunityListBookings200JSONResponse(out), err
}

func (s *apiServer) CommunityCreateBooking(ctx context.Context, req gen.CommunityCreateBookingRequestObject) (gen.CommunityCreateBookingResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	d, err := commBodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	b, err := c.CreateBooking(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Booking](b)
	return gen.CommunityCreateBooking201JSONResponse(out), err
}

func (s *apiServer) CommunityGetBooking(ctx context.Context, req gen.CommunityGetBookingRequestObject) (gen.CommunityGetBookingResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	b, err := c.GetBooking(ctx, req.OrgId, req.BookingId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Booking](b)
	return gen.CommunityGetBooking200JSONResponse(out), err
}

func (s *apiServer) CommunityCancelBooking(ctx context.Context, req gen.CommunityCancelBookingRequestObject) (gen.CommunityCancelBookingResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	reason := ""
	if req.Body != nil && req.Body.Reason != nil {
		reason = *req.Body.Reason
	}
	b, err := c.CancelBooking(ctx, req.OrgId, req.BookingId, reason)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Booking](b)
	return gen.CommunityCancelBooking200JSONResponse(out), err
}

// --- visitors ---

func (s *apiServer) CommunityListVisitorEntries(ctx context.Context, req gen.CommunityListVisitorEntriesRequestObject) (gen.CommunityListVisitorEntriesResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	f := community.EntryFilter{SiteID: req.Params.SiteId, UnitID: req.Params.UnitId, From: req.Params.From, To: req.Params.To, Arrived: req.Params.Arrived}
	if req.Params.ApprovalStatus != nil {
		f.ApprovalStatus = *req.Params.ApprovalStatus
	}
	if req.Params.Q != nil {
		f.Q = *req.Params.Q
	}
	items, next, err := c.ListVisitorEntries(ctx, req.OrgId, f, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.VisitorEntryList](items, next)
	return gen.CommunityListVisitorEntries200JSONResponse(out), err
}

func (s *apiServer) CommunityCreateVisitorEntry(ctx context.Context, req gen.CommunityCreateVisitorEntryRequestObject) (gen.CommunityCreateVisitorEntryResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	d, err := commBodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	v, err := c.CreateVisitorEntry(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.VisitorEntry](v)
	return gen.CommunityCreateVisitorEntry201JSONResponse(out), err
}

func (s *apiServer) CommunityGetVisitorEntry(ctx context.Context, req gen.CommunityGetVisitorEntryRequestObject) (gen.CommunityGetVisitorEntryResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	v, err := c.GetVisitorEntry(ctx, req.OrgId, req.EntryId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.VisitorEntry](v)
	return gen.CommunityGetVisitorEntry200JSONResponse(out), err
}

func (s *apiServer) CommunityUpdateVisitorEntry(ctx context.Context, req gen.CommunityUpdateVisitorEntryRequestObject) (gen.CommunityUpdateVisitorEntryResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	d, err := commBodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	v, err := c.UpdateVisitorEntry(ctx, req.OrgId, req.EntryId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.VisitorEntry](v)
	return gen.CommunityUpdateVisitorEntry200JSONResponse(out), err
}

func (s *apiServer) CommunityCancelVisitorEntry(ctx context.Context, req gen.CommunityCancelVisitorEntryRequestObject) (gen.CommunityCancelVisitorEntryResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	v, err := c.CancelVisitorEntry(ctx, req.OrgId, req.EntryId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.VisitorEntry](v)
	return gen.CommunityCancelVisitorEntry200JSONResponse(out), err
}

func (s *apiServer) CommunityRotateVisitorEntryQr(ctx context.Context, req gen.CommunityRotateVisitorEntryQrRequestObject) (gen.CommunityRotateVisitorEntryQrResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	v, err := c.RotateVisitorEntryQR(ctx, req.OrgId, req.EntryId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.VisitorEntry](v)
	return gen.CommunityRotateVisitorEntryQr200JSONResponse(out), err
}

func (s *apiServer) CommunityRotateRecurringVisitorQr(ctx context.Context, req gen.CommunityRotateRecurringVisitorQrRequestObject) (gen.CommunityRotateRecurringVisitorQrResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	r, err := c.RotateRecurringVisitorQR(ctx, req.OrgId, req.RecurringVisitorId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.RecurringVisitor](r)
	return gen.CommunityRotateRecurringVisitorQr200JSONResponse(out), err
}

func (s *apiServer) CommunityListRecurringVisitors(ctx context.Context, req gen.CommunityListRecurringVisitorsRequestObject) (gen.CommunityListRecurringVisitorsResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	status := ""
	if req.Params.Status != nil {
		status = *req.Params.Status
	}
	items, next, err := c.ListRecurringVisitors(ctx, req.OrgId, req.Params.SiteId, req.Params.UnitId, status, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.RecurringVisitorList](items, next)
	return gen.CommunityListRecurringVisitors200JSONResponse(out), err
}

func (s *apiServer) CommunityCreateRecurringVisitor(ctx context.Context, req gen.CommunityCreateRecurringVisitorRequestObject) (gen.CommunityCreateRecurringVisitorResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	d, err := commBodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	r, err := c.CreateRecurringVisitor(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.RecurringVisitor](r)
	return gen.CommunityCreateRecurringVisitor201JSONResponse(out), err
}

func (s *apiServer) CommunityGetRecurringVisitor(ctx context.Context, req gen.CommunityGetRecurringVisitorRequestObject) (gen.CommunityGetRecurringVisitorResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	r, err := c.GetRecurringVisitor(ctx, req.OrgId, req.RecurringVisitorId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.RecurringVisitor](r)
	return gen.CommunityGetRecurringVisitor200JSONResponse(out), err
}

func (s *apiServer) CommunityUpdateRecurringVisitor(ctx context.Context, req gen.CommunityUpdateRecurringVisitorRequestObject) (gen.CommunityUpdateRecurringVisitorResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	d, err := commBodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	r, err := c.UpdateRecurringVisitor(ctx, req.OrgId, req.RecurringVisitorId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.RecurringVisitor](r)
	return gen.CommunityUpdateRecurringVisitor200JSONResponse(out), err
}

func (s *apiServer) CommunityRevokeRecurringVisitor(ctx context.Context, req gen.CommunityRevokeRecurringVisitorRequestObject) (gen.CommunityRevokeRecurringVisitorResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	r, err := c.RevokeRecurringVisitor(ctx, req.OrgId, req.RecurringVisitorId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.RecurringVisitor](r)
	return gen.CommunityRevokeRecurringVisitor200JSONResponse(out), err
}

func (s *apiServer) CommunityListVisitorApprovalRequests(ctx context.Context, req gen.CommunityListVisitorApprovalRequestsRequestObject) (gen.CommunityListVisitorApprovalRequestsResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	status := ""
	if req.Params.Status != nil {
		status = *req.Params.Status
	}
	items, next, err := c.ListApprovalRequests(ctx, req.OrgId, status, req.Params.UnitId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.VisitorApprovalRequestList](items, next)
	return gen.CommunityListVisitorApprovalRequests200JSONResponse(out), err
}

func (s *apiServer) CommunityDecideVisitorApprovalRequest(ctx context.Context, req gen.CommunityDecideVisitorApprovalRequestRequestObject) (gen.CommunityDecideVisitorApprovalRequestResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	r, err := c.DecideApprovalRequest(ctx, req.OrgId, req.RequestId, string(req.Body.Decision))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.VisitorApprovalRequest](r)
	return gen.CommunityDecideVisitorApprovalRequest200JSONResponse(out), err
}

func (s *apiServer) CommunityListDeliveries(ctx context.Context, req gen.CommunityListDeliveriesRequestObject) (gen.CommunityListDeliveriesResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	status := ""
	if req.Params.Status != nil {
		status = *req.Params.Status
	}
	items, next, err := c.ListDeliveries(ctx, req.OrgId, req.Params.SiteId, req.Params.UnitId, status, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.DeliveryList](items, next)
	return gen.CommunityListDeliveries200JSONResponse(out), err
}

// --- guard console ---

func (s *apiServer) GateListGuards(ctx context.Context, req gen.GateListGuardsRequestObject) (gen.GateListGuardsResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	items, next, err := c.ListGuards(ctx, req.OrgId, req.Params.SiteId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.GuardList](items, next)
	return gen.GateListGuards200JSONResponse(out), err
}

func (s *apiServer) CommunityCreateGuardSession(ctx context.Context, req gen.CommunityCreateGuardSessionRequestObject) (gen.CommunityCreateGuardSessionResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	gs, err := c.CreateGuardSession(ctx, req.OrgId, req.Body.SiteId, req.Body.PersonId, req.Body.Pin)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.GuardSession](gs)
	return gen.CommunityCreateGuardSession201JSONResponse(out), err
}

func (s *apiServer) CommunitySetGuardPin(ctx context.Context, req gen.CommunitySetGuardPinRequestObject) (gen.CommunitySetGuardPinResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	if err := c.SetGuardPin(ctx, req.OrgId, req.PersonId, req.Body.Pin); err != nil {
		return nil, err
	}
	return gen.CommunitySetGuardPin204Response{}, nil
}

func (s *apiServer) CommunityGetGateConsole(ctx context.Context, req gen.CommunityGetGateConsoleRequestObject) (gen.CommunityGetGateConsoleResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	g, err := c.GateConsole(ctx, req.OrgId, req.Params.SiteId, guardToken(req.Params.GuardSessionHeader))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.GateConsole](g)
	return gen.CommunityGetGateConsole200JSONResponse(out), err
}

func (s *apiServer) CommunityListGateVisitors(ctx context.Context, req gen.CommunityListGateVisitorsRequestObject) (gen.CommunityListGateVisitorsResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	f := community.GateVisitorFilter{SiteID: req.Params.SiteId}
	for dst, src := range map[*string]*string{&f.Q: req.Params.Q, &f.Plate: req.Params.Plate, &f.Door: req.Params.UnitDoorNumber, &f.Date: req.Params.Date, &f.ApprovalStatus: req.Params.ApprovalStatus} {
		if src != nil {
			*dst = *src
		}
	}
	items, next, err := c.ListGateVisitors(ctx, req.OrgId, f, guardToken(req.Params.GuardSessionHeader), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.GuardVisitorList](items, next)
	return gen.CommunityListGateVisitors200JSONResponse(out), err
}

func (s *apiServer) CommunityGetGateVisitor(ctx context.Context, req gen.CommunityGetGateVisitorRequestObject) (gen.CommunityGetGateVisitorResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	v, err := c.GetGateVisitor(ctx, req.OrgId, req.EntryId, guardToken(req.Params.GuardSessionHeader))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.GuardVisitor](v)
	return gen.CommunityGetGateVisitor200JSONResponse(out), err
}

func (s *apiServer) CommunityGetGateVisitorCallContact(ctx context.Context, req gen.CommunityGetGateVisitorCallContactRequestObject) (gen.CommunityGetGateVisitorCallContactResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	cc, err := c.GetGateVisitorCallContact(ctx, req.OrgId, req.EntryId, guardToken(req.Params.GuardSessionHeader))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.CallContact](cc)
	return gen.CommunityGetGateVisitorCallContact200JSONResponse(out), err
}

func (s *apiServer) CommunityArriveGateVisitor(ctx context.Context, req gen.CommunityArriveGateVisitorRequestObject) (gen.CommunityArriveGateVisitorResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	v, err := c.ArriveGateVisitor(ctx, req.OrgId, req.EntryId, guardToken(req.Params.GuardSessionHeader))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.GuardVisitor](v)
	return gen.CommunityArriveGateVisitor200JSONResponse(out), err
}

func (s *apiServer) CommunityLeaveGateVisitor(ctx context.Context, req gen.CommunityLeaveGateVisitorRequestObject) (gen.CommunityLeaveGateVisitorResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	v, err := c.LeaveGateVisitor(ctx, req.OrgId, req.EntryId, guardToken(req.Params.GuardSessionHeader))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.GuardVisitor](v)
	return gen.CommunityLeaveGateVisitor200JSONResponse(out), err
}

func (s *apiServer) CommunityScanGateVisitor(ctx context.Context, req gen.CommunityScanGateVisitorRequestObject) (gen.CommunityScanGateVisitorResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	r, err := c.ScanGateVisitor(ctx, req.OrgId, req.Body.Token, guardToken(req.Params.GuardSessionHeader))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ScanResult](r)
	return gen.CommunityScanGateVisitor200JSONResponse(out), err
}

func (s *apiServer) CommunityCreateWalkInVisitor(ctx context.Context, req gen.CommunityCreateWalkInVisitorRequestObject) (gen.CommunityCreateWalkInVisitorResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in := community.WalkIn{SiteID: req.Body.SiteId, UnitID: req.Body.UnitId, Name: req.Body.Name}
	if req.Body.Plate != nil {
		in.Plate = *req.Body.Plate
	}
	v, err := c.CreateWalkInVisitor(ctx, req.OrgId, in, guardToken(req.Params.GuardSessionHeader))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.GuardVisitor](v)
	return gen.CommunityCreateWalkInVisitor201JSONResponse(out), err
}

func (s *apiServer) CommunitySearchGatePlates(ctx context.Context, req gen.CommunitySearchGatePlatesRequestObject) (gen.CommunitySearchGatePlatesResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	items, next, err := c.SearchGatePlates(ctx, req.OrgId, req.Params.SiteId, req.Params.Q, guardToken(req.Params.GuardSessionHeader), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.GatePlateList](items, next)
	return gen.CommunitySearchGatePlates200JSONResponse(out), err
}

func (s *apiServer) GateSearchUnits(ctx context.Context, req gen.GateSearchUnitsRequestObject) (gen.GateSearchUnitsResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	items, next, err := c.SearchGateUnits(ctx, req.OrgId, req.Params.SiteId, req.Params.Q, guardToken(req.Params.GuardSessionHeader), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.GateUnitList](items, next)
	return gen.GateSearchUnits200JSONResponse(out), err
}

func (s *apiServer) CommunityListGateDeliveries(ctx context.Context, req gen.CommunityListGateDeliveriesRequestObject) (gen.CommunityListGateDeliveriesResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	door, status := "", ""
	if req.Params.UnitDoorNumber != nil {
		door = *req.Params.UnitDoorNumber
	}
	if req.Params.Status != nil {
		status = *req.Params.Status
	}
	items, next, err := c.ListGateDeliveries(ctx, req.OrgId, req.Params.SiteId, door, status, guardToken(req.Params.GuardSessionHeader), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.GateDeliveryList](items, next)
	return gen.CommunityListGateDeliveries200JSONResponse(out), err
}

func (s *apiServer) CommunityCreateGateDelivery(ctx context.Context, req gen.CommunityCreateGateDeliveryRequestObject) (gen.CommunityCreateGateDeliveryResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	var desc *string
	if req.Body.Description.IsSpecified() && !req.Body.Description.IsNull() {
		v := req.Body.Description.MustGet()
		desc = &v
	}
	d, err := c.CreateGateDelivery(ctx, req.OrgId, req.Body.SiteId, req.Body.UnitId, desc, guardToken(req.Params.GuardSessionHeader))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.GateDelivery](d)
	return gen.CommunityCreateGateDelivery201JSONResponse(out), err
}

func (s *apiServer) CommunityCollectGateDelivery(ctx context.Context, req gen.CommunityCollectGateDeliveryRequestObject) (gen.CommunityCollectGateDeliveryResponseObject, error) {
	c, err := s.cm()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := c.CollectGateDelivery(ctx, req.OrgId, req.DeliveryId, req.Body.CollectedByName, guardToken(req.Params.GuardSessionHeader))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.GateDelivery](d)
	return gen.CommunityCollectGateDelivery200JSONResponse(out), err
}

// commBodyDoc turns a generated request body into the fields the client sent.
func commBodyDoc(missing bool, body any) (community.Doc, error) {
	if err := hasBody(missing); err != nil {
		return nil, err
	}
	return docOf(body)
}
