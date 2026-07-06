package tests

import (
	"encoding/xml"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/openshift-telco/go-netconf-client/netconf/message"
)

func TestRPCReply(t *testing.T) {

	input, err := os.ReadFile("resources/junos-rpc-reply.xml")
	if err != nil {
		t.Fatalf("failed to read resources: %v", err)
	}

	// validate we can create RPCReply when it's encapsulated in a xmlns
	reply, err := message.NewRPCReply(input)
	if err != nil {
		t.Fatalf("failed to unmarshal rpc reply: %v", err)
	}

	// validate we can marshall the created RPCReply.
	// Marshalling emits the canonical <rpc-reply message-id="..."> envelope
	// wrapping the preserved inner XML; it does not round-trip the server's
	// original namespace-prefixed root element (e.g. <nc:rpc-reply>).
	output, err := xml.Marshal(reply)
	if err != nil {
		t.Fatalf("failed to marshal rpc reply: %v", err)
	}

	expected := `<rpc-reply message-id="">
    <interface-information
            xmlns="http://xml.juniper.net/junos/22.2I0/junos-interface" junos:style="description">
        <physical-interface>
            <name>et-0/1/5</name>
            <admin-status>up</admin-status>
            <oper-status>up</oper-status>
            <description>TEST_Description</description>
        </physical-interface>
    </interface-information>
</rpc-reply>`

	// Normalize line endings so the assertion does not depend on how the
	// resource file was checked out (CRLF vs LF).
	if got, want := normalizeNewlines(string(output)), normalizeNewlines(expected); got != want {
		t.Errorf("got %q, \nwanted %q", got, want)
	}

	_, e := regexp.MatchString(message.RpcReplyRegex, string(input))
	if e != nil {
		t.Errorf("failed to parse rpc-reply with regex")
	}
}

// normalizeNewlines collapses CRLF sequences to LF so string comparisons are
// independent of the platform / git line-ending configuration.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
