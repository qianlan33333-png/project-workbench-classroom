import Foundation
import PDFKit
import Vision

let url = URL(fileURLWithPath: CommandLine.arguments[1])
func output(_ value: [String: Any]) throws {
    let data = try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys, .withoutEscapingSlashes])
    FileHandle.standardOutput.write(data)
}
do {
    if CommandLine.arguments[2] == "pdf" {
        guard let pdf = PDFDocument(url: url), !pdf.isLocked, pdf.pageCount <= 50 else { throw NSError(domain: "invalid PDF", code: 1) }
        var pages: [String] = [], missing: [String] = []
        for i in 0..<pdf.pageCount {
            let text = pdf.page(at: i)?.string ?? ""
            if text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { missing.append(String(i+1)) }
            else { pages.append("第\(i+1)页\n" + text) }
        }
        var result: [String: Any] = ["text": pages.joined(separator: "\n\n"), "kind": "pdf"]
        if !missing.isEmpty { result["warning"] = "部分页面没有可提取文字，可能为扫描页；未读取页：" + missing.joined(separator: ",") }
        try output(result)
    } else {
        let request = VNRecognizeTextRequest()
        request.usesCPUOnly = true
        request.recognitionLevel = .accurate
        request.recognitionLanguages = ["zh-Hans", "en-US"]
        request.usesLanguageCorrection = true
        try VNImageRequestHandler(url: url).perform([request])
        let text = (request.results ?? []).compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
        try output(["text": text, "kind": "ocr"])
    }
} catch { exit(1) }
