package handlers

import (
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestHandleListTokensUsesBoundedOrderedScan(t *testing.T) {
	encode := func(token *pb.Token) []byte {
		raw, err := proto.Marshal(token)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	recorder := httptest.NewRecorder()
	handleListTokensWithScan(recorder, func(prefix string, limit int, reverse bool) ([]interfaces.KVPair, error) {
		if prefix != keys.KeyTokenPrefix() || limit != maxListTokenRows+2 || reverse {
			t.Fatalf("scan prefix=%q limit=%d reverse=%v", prefix, limit, reverse)
		}
		return []interfaces.KVPair{
			{Key: keys.KeyToken("btc"), Value: encode(&pb.Token{Address: "btc", Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "btc"})},
			{Key: keys.KeyTokenRegistry(), Value: []byte("metadata")},
			{Key: keys.KeyToken("fb"), Value: encode(&pb.Token{Address: "fb", Type: pb.TokenType_TOKEN_SYSTEM})},
		}, nil
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var tokens []tokenSummary
	if err := json.Unmarshal(recorder.Body.Bytes(), &tokens); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(tokens) != 2 || tokens[0].Address != "fb" || tokens[1].Address != "btc" {
		t.Fatalf("unexpected sorted tokens: %+v", tokens)
	}
}

func TestHandleListTokensRejectsOverflow(t *testing.T) {
	recorder := httptest.NewRecorder()
	handleListTokensWithScan(recorder, func(string, int, bool) ([]interfaces.KVPair, error) {
		rows := make([]interfaces.KVPair, maxListTokenRows+1)
		for i := range rows {
			rows[i].Key = keys.KeyToken("token-" + string(rune(i+1)))
		}
		return rows, nil
	})
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusRequestEntityTooLarge)
	}
}

func TestHandleListTokensPaginationSkipsRegistryAndContinuesByAddress(t *testing.T) {
	encode := func(address string) []byte {
		raw, err := proto.Marshal(&pb.Token{Address: address})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	rows := []interfaces.KVPair{
		{Key: keys.KeyToken("a"), Value: encode("a")},
		{Key: keys.KeyToken("b"), Value: encode("b")},
		{Key: keys.KeyTokenRegistry(), Value: []byte("metadata")},
		{Key: keys.KeyToken("s"), Value: encode("s")},
		{Key: keys.KeyToken("z"), Value: encode("z")},
	}
	scan := func(prefix, startAfter string, limit int, reverse bool) ([]interfaces.KVPair, error) {
		if prefix != keys.KeyTokenPrefix() || limit != 4 || reverse {
			t.Fatalf("scan prefix=%q startAfter=%q limit=%d reverse=%v", prefix, startAfter, limit, reverse)
		}
		out := make([]interfaces.KVPair, 0, limit)
		for _, row := range rows {
			if startAfter != "" && row.Key <= startAfter {
				continue
			}
			out = append(out, row)
			if len(out) == limit {
				break
			}
		}
		return out, nil
	}

	first := httptest.NewRecorder()
	handleListTokensPageWithScan(first, httptest.NewRequest(http.MethodGet, "/listtokens?limit=2", nil), scan)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var firstPage tokenPageResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstPage); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(firstPage.Tokens) != 2 || firstPage.Tokens[0].Address != "a" || firstPage.Tokens[1].Address != "b" || firstPage.NextCursor != "b" {
		t.Fatalf("unexpected first page: %+v", firstPage)
	}

	second := httptest.NewRecorder()
	handleListTokensPageWithScan(second, httptest.NewRequest(http.MethodGet, "/listtokens?limit=2&cursor="+firstPage.NextCursor, nil), scan)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	var secondPage tokenPageResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondPage); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(secondPage.Tokens) != 2 || secondPage.Tokens[0].Address != "s" || secondPage.Tokens[1].Address != "z" || secondPage.NextCursor != "" {
		t.Fatalf("unexpected second page: %+v", secondPage)
	}
}

func TestHandleListTokensPaginationBounds(t *testing.T) {
	tests := []struct {
		name      string
		target    string
		wantLimit int
	}{
		{name: "default", target: "/listtokens?cursor=", wantLimit: defaultListTokenPageSize + 2},
		{name: "maximum", target: "/listtokens?limit=999999", wantLimit: maxListTokenPageSize + 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handleListTokensPageWithScan(recorder, httptest.NewRequest(http.MethodGet, tc.target, nil), func(prefix, startAfter string, limit int, reverse bool) ([]interfaces.KVPair, error) {
				if prefix != keys.KeyTokenPrefix() || startAfter != "" || limit != tc.wantLimit || reverse {
					t.Fatalf("scan prefix=%q startAfter=%q limit=%d reverse=%v", prefix, startAfter, limit, reverse)
				}
				return nil, nil
			})
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var page tokenPageResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Tokens == nil {
				t.Fatal("tokens must be an empty array, not null")
			}
		})
	}
}

func TestHandleListTokensPaginationRejectsInvalidLimit(t *testing.T) {
	for _, raw := range []string{"0", "-1", "invalid"} {
		recorder := httptest.NewRecorder()
		handleListTokensPageWithScan(recorder, httptest.NewRequest(http.MethodGet, "/listtokens?limit="+raw, nil), func(string, string, int, bool) ([]interfaces.KVPair, error) {
			t.Fatal("invalid limit must not reach storage")
			return nil, nil
		})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("limit=%q status=%d body=%s", raw, recorder.Code, recorder.Body.String())
		}
	}
}
