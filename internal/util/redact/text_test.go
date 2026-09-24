package redact

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestText(t *testing.T) {
	cases := map[string]string{
		// строка из логов sms (реальный формат)
		`fail to send, account: service, body: , phone: 77021330032, message: Активация`: `fail to send, account: service, body: , phone: ***, message: Активация`,
		`sent to 77021330032 ok`:               `sent to <PHONE> ok`,
		`call +7 (702) 133-00-32 failed`:       `call <PHONE> failed`,
		`call 8 702 133 00 32`:                 `call <PHONE>`,
		`intl +442071838750`:                   `intl <PHONE>`,
		`user ivan.petrov@mechta.kz not found`: `user <EMAIL> not found`,
		`{"email":"a@b.kz","id":1}`:            `{"email":"***","id":1}`,
		`pay by 4111 1111 1111 1111 declined`:  `pay by <CARD> declined`,
		`pay by 4111111111111111`:              `pay by <CARD>`,
		`"card_number": "5500-0000-0000-0004"`: `"card_number": "***"`,
		// не PII: идентификаторы, время, IP, короткие числа
		`order 1234567890123456 created`:                 `order 1234567890123456 created`,
		`dial tcp 192.168.85.3:25014: i/o timeout`:       `dial tcp 192.168.85.3:25014: i/o timeout`,
		`took 20s, retry 3 of 100`:                       `took 20s, retry 3 of 100`,
		`2026-09-24T14:05:33+05:00 done`:                 `2026-09-24T14:05:33+05:00 done`,
		`trace 9becdd36171015cbe8d3783a08f7dc4f977faa31`: `trace 9becdd36171015cbe8d3783a08f7dc4f977faa31`,
	}
	for in, want := range cases {
		assert.Equal(t, want, Text(in), in)
	}
}
