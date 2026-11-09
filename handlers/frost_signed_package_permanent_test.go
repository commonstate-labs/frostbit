package handlers

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestDownloadSignedPackagePermanentStateAndLegacyFallback(t *testing.T) {
	for _, name := range []string{"state-only", "state-before-legacy", "legacy-active", "legacy-archive", "legacy-marker", "missing", "state-empty", "state-malformed", "state-wrong-job", "state-wrong-index", "state-no-signature"} {
		t.Run(name, func(t *testing.T) {
			hm, dbm := newFrostQueryTestHandler(t)
			defer dbm.Close()
			pkg := &pb.FrostSignedPackage{JobId: "job-state", Idx: 3, SignedPackageBytes: []byte("signature"), SubmitHeight: 42, Submitter: "signer"}
			permanent := keys.KeyFrostPermanentSignedPackage(pkg.JobId, pkg.Idx)
			active := keys.KeyFrostSignedPackage(pkg.JobId, pkg.Idx)
			archive := keys.KeyArchiveFrostSignedPackage(pkg.JobId, pkg.Idx)
			want := http.StatusOK
			switch name {
			case "state-only":
				enqueueProto(t, dbm, permanent, pkg)
			case "state-before-legacy":
				enqueueProto(t, dbm, permanent, pkg)
				dbm.EnqueueSet(active, "invalid")
				dbm.EnqueueSet(archive, "invalid")
			case "legacy-active":
				enqueueProto(t, dbm, active, pkg)
			case "legacy-archive":
				enqueueProto(t, dbm, archive, pkg)
			case "legacy-marker":
				enqueueProto(t, dbm, active, &pb.FrostSignedPackage{JobId: pkg.JobId, Idx: pkg.Idx})
				enqueueProto(t, dbm, archive, pkg)
			case "missing":
				want = http.StatusNotFound
			default:
				want = http.StatusInternalServerError
				                                                                
				enqueueProto(t, dbm, active, pkg)
				enqueueProto(t, dbm, archive, pkg)
				broken := proto.Clone(pkg).(*pb.FrostSignedPackage)
				switch name {
				case "state-empty":
					dbm.EnqueueSet(permanent, "")
				case "state-malformed":
					dbm.EnqueueSet(permanent, "invalid")
				case "state-wrong-job":
					broken.JobId = "other"
					enqueueProto(t, dbm, permanent, broken)
				case "state-wrong-index":
					broken.Idx++
					enqueueProto(t, dbm, permanent, broken)
				case "state-no-signature":
					broken.SignedPackageBytes = nil
					enqueueProto(t, dbm, permanent, broken)
				}
			}
			if err := dbm.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRecorder()
			hm.HandleDownloadSignedPackage(r, httptest.NewRequest(http.MethodGet, "/frost/withdraw/download?job_id=job-state&idx=3", nil))
			if r.Code != want {
				t.Fatalf("status=%d want=%d body=%s", r.Code, want, r.Body.String())
			}
			if want == http.StatusOK {
				var got pb.DownloadSignedPackageResponse
				if err := proto.Unmarshal(r.Body.Bytes(), &got); err != nil || got.JobId != pkg.JobId || got.Idx != pkg.Idx || got.SignedPackageBytes != hex.EncodeToString(pkg.SignedPackageBytes) || got.SubmitHeight != pkg.SubmitHeight || got.Submitter != pkg.Submitter {
					t.Fatalf("response=%+v err=%v", &got, err)
				}
			}
		})
	}
}
