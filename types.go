package main

// ======================= CONFIG =======================

const (
	defaultAddr = ":8080"
	defaultOut  = "merged_output" // nơi lưu file merge đã tạo
	defaultData = "items.jsonl"   // file dữ liệu đầu vào
)

// ======================= DATA TYPES ===================

type Item struct {
	Title   string `json:"title"`
	PDFURL  string `json:"pdf_url"`
	IMGURL  string `json:"img_url"`
	URL     string `json:"detail_url,omitempty"`
	Subject string `json:"subject,omitempty"`
}

type MergeRequest struct {
	Files []string `json:"files"` // danh sách PDF URL
	Out   string   `json:"out"`   // tên file output (không có .pdf)
}
