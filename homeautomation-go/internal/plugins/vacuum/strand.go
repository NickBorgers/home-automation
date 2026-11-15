package vacuum

import (
	"fmt"
	"time"

	"homeautomation/internal/valetudo"

	"go.uber.org/zap"
)

// checkStranded sends home a robot that stands idle or paused away from the dock while no run is tracked, for
// longer than stranded_after. Such a robot was left behind: a run that ended while the robot was stopped, or
// a job someone stopped by hand. Nothing else would ever send it home, and the planner starts nothing while
// the robot is not docked.
//
// It goes home only when its shortest way home enters no no_enter room, as for our own runs. Where it is comes
// from the map, else from the last run's last room. A robot whose room is not known stays where it is. If it
// is still standing there stranded_after later, it is sent home again. Only an active scheduler sends
// anything; command() also keeps a read-only app from sending. The caller holds tickMu.
func (s *Scheduler) checkStranded(now time.Time, st *valetudo.State) {
	stopped := (st.Status == valetudo.StatusIdle || st.Status == valetudo.StatusPaused) &&
		st.DockStatus != valetudo.DockStatusCleaning
	if s.cfg.Mode != ModeActive || !stopped {
		s.strandSince, s.strandHomeAt = time.Time{}, time.Time{}
		return
	}
	if s.strandSince.IsZero() {
		s.strandSince = now
	}
	stood := now.Sub(s.strandSince)
	if stood < s.cfg.StrandedAfter {
		return
	}
	if !s.strandHomeAt.IsZero() && now.Sub(s.strandHomeAt) < s.cfg.StrandedAfter {
		return
	}
	// The map is read only when the robot may be sent home now.
	room, posOK := s.locate()
	if posOK {
		s.robotRoom = room
	}
	at := room
	if (!posOK || !s.graph.Has(at)) && s.lastRun != nil {
		at = s.lastRun.lastKnownRoom
	}
	if !s.graph.Has(at) {
		s.warn("stranded", fmt.Sprintf("robot is %s away from the dock for %s and its room is not known; it is "+
			"not sent home", st.Status, shortDuration(stood)))
		return
	}
	restr := ComputeRestrictions(s.graph, s.blockedAreas(), s.sleeperRooms())
	crossed, ok := LegCrosses(s.graph, at, s.dockRoom, restr.NoEnter)
	if !ok || crossed != "" {
		msg := fmt.Sprintf("robot is %s in %s for %s with no run; ", st.Status, at, shortDuration(stood))
		if !ok {
			msg += "there is no way home"
		} else {
			msg += "its way home enters " + crossed + " (" + restr.NoEnter[crossed] + "); waiting"
		}
		s.warn("stranded", msg)
		return
	}
	why := fmt.Sprintf("robot stood %s in %s for %s with no run; way home is clear", st.Status, at,
		shortDuration(stood))
	s.logger.Warn("Vacuum robot left standing away from the dock; sending it home", zap.String("room", at),
		zap.String("status", st.Status), zap.String("flag", st.Flag), zap.Duration("stood", stood))
	if s.command(valetudo.ActionHome, why) == nil {
		s.strandHomeAt = now
	}
}
