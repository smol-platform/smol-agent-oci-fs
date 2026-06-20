import Darwin
import Foundation
import FSKit

struct OSIxMountOptionsValidationError: Error, CustomStringConvertible {
    let description: String
}

struct OSIxMountOptions {
    let bundle: String?
    let workspace: String?
    let sourceRef: String?
    let sourceDigest: String?
    let lower: String?
    let upper: String?
    let work: String?
    let mode: String?

    static func parse(_ options: FSTaskOptions) -> OSIxMountOptions {
        let values = parseKeyValues(options.taskOptions)
        return OSIxMountOptions(
            bundle: values["bundle"],
            workspace: values["workspace"],
            sourceRef: values["source_ref"],
            sourceDigest: values["source_digest"],
            lower: values["lower"],
            upper: values["upper"],
            work: values["work"],
            mode: values["mode"]
        )
    }

    func validateForMount() throws {
        for (key, value) in [
            ("workspace", workspace),
            ("source_ref", sourceRef),
            ("source_digest", sourceDigest),
            ("lower", lower),
            ("upper", upper),
            ("work", work),
            ("mode", mode),
        ] where value?.isEmpty ?? true {
            throw OSIxMountOptionsValidationError(description: "missing required mount option osix.\(key)")
        }

        switch mode {
        case "overlay", "fuse":
            break
        default:
            throw OSIxMountOptionsValidationError(description: "unsupported OSIx FSKit mount mode \(mode ?? "")")
        }

        try validateDirectory(path: workspace!, option: "osix.workspace")
        try validateDirectory(path: lower!, option: "osix.lower")
        try validateDirectory(path: upper!, option: "osix.upper")
        try validateDirectory(path: work!, option: "osix.work")
        try validateRuntimeDirectoriesAreDisjoint()
        try validateSourceDigest(sourceDigest!)
    }

    private func validateDirectory(path: String, option: String) throws {
        var statBuffer = stat()
        guard lstat(path, &statBuffer) == 0 else {
            throw OSIxMountOptionsValidationError(description: "\(option) \(path) is unavailable: \(String(cString: strerror(errno)))")
        }
        guard statBuffer.st_mode & S_IFMT == S_IFDIR else {
            throw OSIxMountOptionsValidationError(description: "\(option) \(path) is not a directory")
        }
    }

    private func validateRuntimeDirectoriesAreDisjoint() throws {
        let directories = [
            ("osix.lower", canonicalPath(lower!)),
            ("osix.upper", canonicalPath(upper!)),
            ("osix.work", canonicalPath(work!)),
        ]
        for index in directories.indices {
            for otherIndex in directories.indices where otherIndex > index {
                let current = directories[index]
                let other = directories[otherIndex]
                if current.1 == other.1 || isNestedPath(current.1, under: other.1) || isNestedPath(other.1, under: current.1) {
                    throw OSIxMountOptionsValidationError(description: "\(current.0) and \(other.0) must be separate directories")
                }
            }
        }
    }

    private func canonicalPath(_ path: String) -> String {
        URL(fileURLWithPath: path).resolvingSymlinksInPath().standardizedFileURL.path
    }

    private func isNestedPath(_ path: String, under parent: String) -> Bool {
        path.hasPrefix(parent.hasSuffix("/") ? parent : parent + "/")
    }

    private func validateSourceDigest(_ digest: String) throws {
        let prefix = "sha256:"
        guard digest.hasPrefix(prefix) else {
            throw OSIxMountOptionsValidationError(description: "osix.source_digest must be a sha256 digest")
        }
        let hex = digest.dropFirst(prefix.count)
        let hexDigits = CharacterSet(charactersIn: "0123456789abcdefABCDEF")
        guard hex.count == 64, hex.unicodeScalars.allSatisfy({ hexDigits.contains($0) }) else {
            throw OSIxMountOptionsValidationError(description: "osix.source_digest must be a sha256 digest")
        }
    }

    private static func parseKeyValues(_ taskOptions: [String]) -> [String: String] {
        var values: [String: String] = [:]
        var index = 0
        while index < taskOptions.count {
            let option = taskOptions[index]
            if option == "-o", index + 1 < taskOptions.count {
                merge(optionString: taskOptions[index + 1], into: &values)
                index += 2
                continue
            }
            if option.hasPrefix("osix.") {
                merge(optionString: option, into: &values)
            }
            index += 1
        }
        return values
    }

    private static func merge(optionString: String, into values: inout [String: String]) {
        for rawPair in optionString.split(separator: ",") {
            let pair = rawPair.split(separator: "=", maxSplits: 1).map(String.init)
            guard pair.count == 2, pair[0].hasPrefix("osix.") else {
                continue
            }
            let key = String(pair[0].dropFirst("osix.".count))
            values[key] = decodeBase64URL(pair[1])
        }
    }

    private static func decodeBase64URL(_ value: String) -> String {
        var base64 = value
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        let remainder = base64.count % 4
        if remainder != 0 {
            base64 += String(repeating: "=", count: 4 - remainder)
        }
        guard let data = Data(base64Encoded: base64),
              let decoded = String(data: data, encoding: .utf8) else {
            return value
        }
        return decoded
    }
}
