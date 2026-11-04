package k8s

import "testing"

func TestRunAs(t *testing.T) {
	i := func(v int64) *int64 { return &v }
	cases := []struct {
		pod  *SecContext
		c    *SecContext
		want string
	}{
		{nil, nil, ""},
		{&SecContext{RunAsUser: i(0)}, nil, "0"},
		{&SecContext{RunAsUser: i(0)}, &SecContext{RunAsUser: i(10001)}, "10001"},
		{&SecContext{RunAsUser: i(1000), RunAsGroup: i(1000)}, nil, "1000:1000"},
		{&SecContext{RunAsGroup: i(5)}, nil, ""},
	}
	for _, tc := range cases {
		got := RunAs(PodSpec{SecurityContext: tc.pod}, Container{SecurityContext: tc.c})
		if got != tc.want {
			t.Errorf("RunAs(%+v, %+v) = %q, want %q", tc.pod, tc.c, got, tc.want)
		}
	}
}
