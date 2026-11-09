package consensus

import "testing"

                                                                                       
                                                                                                         
func TestFormalLivenessReportOrigin(t *testing.T) {
	t.Run("authenticated-chit-and-known-send-failure", TestAuthenticatedRealResponsesAloneProduceOnlineReports)
	t.Run("authenticated-probe-and-expired-session", TestAuthenticatedProbeReplyReportsOnlineWithoutEnteringConsensus)
}
