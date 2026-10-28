package services

// 品牌 Logo 上传的纯函数部分：文件头嗅探 + URL 净化。
// 这两个是安全边界（决定"什么文件能进 /media/brand/"、"什么地址会被塞进 <img src>"），
// 必须用测试钉死，不依赖数据库。

import (
	"bytes"
	"strings"
	"testing"
)

func TestSniffImageExtFromReader(t *testing.T) {
	pngHeader := []byte("\x89PNG\r\n\x1a\n")
	jpgHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	webpHeader := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
	svgContent := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	phpDisguised := []byte("<?php system($_GET['c']); ?>")

	cases := []struct {
		name    string
		payload []byte
		want    string
		wantErr bool
	}{
		{"png", append(pngHeader, make([]byte, 64)...), "png", false},
		{"jpg", append(jpgHeader, make([]byte, 64)...), "jpg", false},
		{"webp", webpHeader, "webp", false},
		{"svg 必须被拒", svgContent, "", true},
		{"伪装成 png 的 php 必须被拒", phpDisguised, "", true},
		{"空内容", []byte{}, "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sniffImageExtFromReader(bytes.NewReader(tc.payload))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际返回扩展名 %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望报错，实际: %v", err)
			}
			if got != tc.want {
				t.Fatalf("扩展名不符：期望 %q，实际 %q", tc.want, got)
			}
		})
	}
}

func TestSanitizeBrandURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"正常值", "/media/brand/login_1730000000.png", "/media/brand/login_1730000000.png"},
		{"空值", "", ""},
		{"纯空白", "   ", ""},
		{"外链必须被拒", "https://evil.example.com/a.png", ""},
		{"javascript 伪协议必须被拒", "javascript:alert(1)", ""},
		{"目录穿越必须被拒", "/media/brand/../../etc/passwd", ""},
		{"多级路径必须被拒", "/media/brand/sub/a.png", ""},
		{"非 brand 目录必须被拒", "/media/avatars/admin_1.jpg", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeBrandURL(tc.raw); got != tc.want {
				t.Fatalf("净化结果不符：期望 %q，实际 %q", tc.want, got)
			}
		})
	}
}

func TestConfigKeyOf(t *testing.T) {
	if key, ok := configKeyOf("login"); !ok || key != LoginLogoConfigKey {
		t.Fatalf("login 映射错误: %q %v", key, ok)
	}
	if key, ok := configKeyOf("sidebar"); !ok || key != SidebarLogoConfigKey {
		t.Fatalf("sidebar 映射错误: %q %v", key, ok)
	}
	if _, ok := configKeyOf(""); ok {
		t.Fatal("空 type 应当非法")
	}
	if _, ok := configKeyOf(strings.ToUpper("LOGIN")); ok {
		t.Fatal("type 区分大小写，LOGIN 应当非法")
	}
}

func TestNormalizeBrandText(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"正常值", "JaySQL DevOps", "JaySQL DevOps"},
		{"中文品牌名", "数据库运维平台", "数据库运维平台"},
		{"空值回落默认", "", ""},
		{"纯空白回落默认", "   ", ""},
		{"首尾空白裁掉", "  JaySQL  ", "JaySQL"},
		{"换行压成空格", "JaySQL\nDevOps", "JaySQL DevOps"},
		{"制表符压成空格", "JaySQL\tDevOps", "JaySQL DevOps"},
		{"控制字符剔除", "Jay\x01SQL", "JaySQL"},
		{"超长直接回落默认", strings.Repeat("字", 33), ""},
		{"上限内保留", strings.Repeat("字", 32), strings.Repeat("字", 32)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeBrandText(tc.raw, maxBrandTextLen); got != tc.want {
				t.Fatalf("净化结果不符：期望 %q，实际 %q", tc.want, got)
			}
		})
	}
}
