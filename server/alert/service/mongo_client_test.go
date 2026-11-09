package service

import "testing"

func TestNormalizeMongoURI(t *testing.T) {
	cases := []struct{ name, in, authDB, want string }{
		{"内嵌+库名+无authSource→注入admin",
			"mongodb://root:p@h:27017/mdwsrows", "",
			"mongodb://root:p@h:27017/mdwsrows?authSource=admin"},
		{"内嵌+库名+无authSource→用authDB",
			"mongodb://root:p@h:27017/mdwsrows", "myauth",
			"mongodb://root:p@h:27017/mdwsrows?authSource=myauth"},
		{"已有authSource→不动",
			"mongodb://root:p@h:27017/mdwsrows?authSource=admin", "",
			"mongodb://root:p@h:27017/mdwsrows?authSource=admin"},
		{"无库名→不动(驱动默认admin)",
			"mongodb://root:p@h:27017", "",
			"mongodb://root:p@h:27017"},
		{"无内嵌凭据→不动(走SetAuth路径)",
			"mongodb://h:27017/mdwsrows", "",
			"mongodb://h:27017/mdwsrows"},
	}
	for _, c := range cases {
		if got := normalizeMongoURI(c.in, c.authDB); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
