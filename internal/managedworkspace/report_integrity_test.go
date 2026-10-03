package managedworkspace

import "testing"

func TestStrictMetadataRejectsAmbiguity(t *testing.T) {
	for _, data := range []string{
		`{"version":1,"status":"unknown","status":"succeeded","operations":[]}`,
		`{"version":1,"status":"unknown","st\u0061tus":"succeeded","operations":[]}`,
		`{"version":1,"status":"succeeded","operations":null}`,
		`{"version":1,"status":null,"operations":[]}`,
		`{"version":1,"Status":"succeeded","operations":[]}`,
		`{"version":1,"status":"succeeded","operations":[]} trailing`,
	} {
		for i := 0; i < 10; i++ {
			var report Report
			if strictMetadata([]byte(data), &report) == nil {
				t.Fatal("ambiguous metadata accepted")
			}
		}
	}
}

func TestRunIDRejectsTraversal(t *testing.T) {
	if !validID("0123456789abcdef0123456789abcdef") {
		t.Fatal("valid ID rejected")
	}
	for _, id := range []string{"", "../escape", "/absolute", `..\escape`, "0123456789abcdef0123456789abcdeF", "0123456789abcdef0123456789abcdef/", "0123456789abcdef0123456789abcde\x00"} {
		if validID(id) {
			t.Fatal("unsafe ID accepted")
		}
	}
}
