package guard

import (
	"reflect"
	"testing"
)

func TestBioLinks(t *testing.T) {
	cases := []struct {
		bio, ch string
		want    []string
	}{
		{"喜欢摄影和猫", "", nil},
		{"邮箱 a@b.com", "", nil},
		{"福利 https://t.me/AbcGroup/ 速来", "", []string{"t.me/abcgroup"}},
		{"t.me/abcgroup/123 和 @AbcGroup", "", []string{"t.me/abcgroup"}},
		{"进群 t.me/+AbCdEf", "", []string{"t.me/+AbCdEf"}},
		{"官网 https://www.Example.com/x?y=1", "MyChan", []string{"example.com/x?y=1", "t.me/mychan"}},
		{"tg://resolve?domain=Spam_Bot", "", []string{"t.me/spam_bot"}},
	}
	for _, c := range cases {
		if got := bioLinks(c.bio, c.ch); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v want %v", c.bio, got, c.want)
		}
	}
}
