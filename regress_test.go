package imapsql

import (
	"io/ioutil"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"
	"gotest.tools/assert"
	is "gotest.tools/assert/cmp"
)

const (
	testMsgHeader = "From: <foxcpp@foxcpp.dev>\r\n" +
		"Subject: Hello!\r\n" +
		"Content-Type: text/plain; charset=ascii\r\n" +
		"Non-Cached-Header: 1\r\n" +
		"\r\n"
	testMsgBody = "Hello!\r\n"
	testMsg     = testMsgHeader +
		testMsgBody
)

type collectorConn struct{
	upds []backend.Update
}

func (c *collectorConn) SendUpdate(upd backend.Update) error {
	c.upds = append(c.upds, upd)
	return nil
}

func TestRecentIncorrectReset(t *testing.T) {
	b := initTestBackend()
	defer cleanBackend(b)
	assert.NilError(t, b.CreateUser(t.Name()))
	usr, err := b.GetUser(t.Name())
	assert.NilError(t, err)
	assert.NilError(t, usr.CreateMailbox(t.Name()))

	for i := 0; i < 5; i++ {
		assert.NilError(t, usr.CreateMessage(t.Name(), []string{"flag1", "flag2"}, time.Now(), strings.NewReader(testMsg), nil))
	}

	conn := collectorConn{}
	info, mbox, err := usr.GetMailbox(t.Name(), false, &conn)
	assert.NilError(t, err)
	assert.Equal(t, info.Messages, uint32(5))
	assert.Equal(t, info.Recent, uint32(5))

	assert.NilError(t, usr.CreateMessage(t.Name(), []string{"flag1", "flag2"}, time.Now(), strings.NewReader(testMsg), mbox))
	assert.NilError(t, mbox.Poll(true))
	assert.Equal(t, conn.upds[1].(*backend.MailboxUpdate).Recent, uint32(6))

	assert.NilError(t, mbox.Close())
	info, mbox, err = usr.GetMailbox(t.Name(), false, &conn)
	assert.NilError(t, err)
	assert.Equal(t, info.Messages, uint32(6))
	assert.Equal(t, info.Recent, uint32(0))
}

func TestIssue7(t *testing.T) {
	b := initTestBackend()
	defer cleanBackend(b)
	assert.NilError(t, b.CreateUser(t.Name()))
	usr, err := b.GetUser(t.Name())
	assert.NilError(t, err)
	assert.NilError(t, usr.CreateMailbox(t.Name()))
	_, mbox, err := usr.GetMailbox(t.Name(), true, &noopConn{})
	assert.NilError(t, err)
	for i := 0; i < 5; i++ {
		assert.NilError(t, usr.CreateMessage(mbox.Name(), []string{"flag1", "flag2"}, time.Now(), strings.NewReader(testMsg), nil))
	}

	t.Run("seq", func(t *testing.T) {
		crit := imap.SearchCriteria{}
		seqs, err := mbox.SearchMessages(false, &crit)
		assert.NilError(t, err)

		t.Log("Seq. nums.:", seqs)

		seenSeq := make(map[uint32]bool)
		for _, seq := range seqs {
			assert.Check(t, !seenSeq[seq], "Duplicate sequence number in SEARCH ALL response")
			seenSeq[seq] = true
		}
	})
	t.Run("uid", func(t *testing.T) {
		crit := imap.SearchCriteria{}
		uids, err := mbox.SearchMessages(true, &crit)
		assert.NilError(t, err)

		t.Log("UIDs:", uids)

		seenUids := make(map[uint32]bool)
		for _, uid := range uids {
			assert.Check(t, !seenUids[uid], "Duplicate UID in SEARCH ALL response")
			seenUids[uid] = true
		}
	})
}

func TestDuplicateSearchWithoutFlags(t *testing.T) {
	b := initTestBackend()
	defer cleanBackend(b)
	assert.NilError(t, b.CreateUser(t.Name()))
	usr, err := b.GetUser(t.Name())
	assert.NilError(t, err)
	assert.NilError(t, usr.CreateMailbox(t.Name()))
	_, mbox, err := usr.GetMailbox(t.Name(), true, &noopConn{})
	assert.NilError(t, err)
	for i := 0; i < 5; i++ {
		assert.NilError(t, usr.CreateMessage(mbox.Name(), []string{"flag1", "flag2"}, time.Now(), strings.NewReader(testMsg), mbox))
	}
	assert.NilError(t, mbox.Poll(true))

	res, err := mbox.SearchMessages(true, &imap.SearchCriteria{
		WithoutFlags: []string{"flag3"},
	})
	assert.NilError(t, err)
	assert.DeepEqual(t, res, []uint32{1, 2, 3, 4, 5})

	res, err = mbox.SearchMessages(false, &imap.SearchCriteria{
		WithoutFlags: []string{"flag3"},
	})
	assert.NilError(t, err)
	assert.DeepEqual(t, res, []uint32{1, 2, 3, 4, 5})
}

func TestHeaderInMultipleBodyFetch(t *testing.T) {
	test := func(t *testing.T, fetchItems []imap.FetchItem) {
		b := initTestBackend()
		defer cleanBackend(b)
		assert.NilError(t, b.CreateUser(t.Name()))
		usr, err := b.GetUser(t.Name())
		assert.NilError(t, err)
		assert.NilError(t, usr.CreateMailbox(t.Name()))
		_, mbox, err := usr.GetMailbox(t.Name(), true, &noopConn{})
		assert.NilError(t, err)
		for i := 0; i < 5; i++ {
			assert.NilError(t, usr.CreateMessage(mbox.Name(), []string{}, time.Now(), strings.NewReader(testMsg), nil))
		}
		assert.NilError(t, mbox.Poll(true))

		seq, _ := imap.ParseSeqSet("1")
		ch := make(chan *imap.Message, 5)
		assert.NilError(t, mbox.ListMessages(false, seq, fetchItems, ch), "ListMessages")
		assert.Assert(t, is.Len(ch, 1))
		msg := <-ch

		for name, literal := range msg.Body {
			blob, err := ioutil.ReadAll(literal)
			assert.NilError(t, err, "ReadAll literal")
			switch name.FetchItem() {
			case "BODY.PEEK[HEADER]":
				assert.Equal(t, string(blob), testMsgHeader)
			case "BODY.PEEK[TEXT]":
				assert.Equal(t, string(blob), testMsgBody)
			}
		}
	}

	t.Run("text/text", func(t *testing.T) {
		test(t, []imap.FetchItem{"BODY.PEEK[TEXT]", "BODY.PEEK[TEXT]"})
	})
	t.Run("header/text", func(t *testing.T) {
		test(t, []imap.FetchItem{"BODY.PEEK[HEADER]", "BODY.PEEK[TEXT]"})
	})
}

func TestHeaderCacheReuse(t *testing.T) {
	b := initTestBackend()
	defer cleanBackend(b)
	assert.NilError(t, b.CreateUser(t.Name()))
	usr, err := b.GetUser(t.Name())
	assert.NilError(t, err)
	assert.NilError(t, usr.CreateMailbox(t.Name()))
	_, mbox, err := usr.GetMailbox(t.Name(), true, &noopConn{})
	assert.NilError(t, err)

	testComplete := "Subject: Test\r\n\r\nBody text"
	testMissingSubject := "Another-Field: Test\r\n\r\nBody text"

	assert.NilError(t, usr.CreateMessage(mbox.Name(), []string{}, time.Now(), strings.NewReader(testComplete), nil))
	assert.NilError(t, usr.CreateMessage(mbox.Name(), []string{}, time.Now(), strings.NewReader(testMissingSubject), nil))
	assert.NilError(t, mbox.Poll(true))

	t.Run("envelope", func(t *testing.T) {
		seq, _ := imap.ParseSeqSet("1:*")
		ch := make(chan *imap.Message, 2)
		assert.NilError(t, mbox.ListMessages(false, seq, []imap.FetchItem{imap.FetchEnvelope}, ch), "ListMessages")
		assert.Assert(t, is.Len(ch, 2))
		<-ch
		msg2 := <-ch

		assert.DeepEqual(t, msg2.Envelope.Subject, "")
	})
}
