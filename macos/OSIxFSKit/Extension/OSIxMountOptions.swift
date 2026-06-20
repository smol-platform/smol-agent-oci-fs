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
    let rw: String?
    let malformedOptions: [String]

    init(
        bundle: String?,
        workspace: String?,
        sourceRef: String?,
        sourceDigest: String?,
        lower: String?,
        upper: String?,
        work: String?,
        mode: String?,
        rw: String? = nil,
        malformedOptions: [String] = []
    ) {
        self.bundle = bundle
        self.workspace = workspace
        self.sourceRef = sourceRef
        self.sourceDigest = sourceDigest
        self.lower = lower
        self.upper = upper
        self.work = work
        self.mode = mode
        self.rw = rw
        self.malformedOptions = malformedOptions
    }

    static func parse(_ options: FSTaskOptions) -> OSIxMountOptions {
        parseTaskOptions(options.taskOptions)
    }

    static func parseTaskOptions(_ taskOptions: [String]) -> OSIxMountOptions {
        let parsed = parseKeyValues(taskOptions)
        let values = parsed.values
        return OSIxMountOptions(
            bundle: values["bundle"],
            workspace: values["workspace"],
            sourceRef: values["source_ref"],
            sourceDigest: values["source_digest"],
            lower: values["lower"],
            upper: values["upper"],
            work: values["work"],
            mode: values["mode"],
            rw: values["rw"],
            malformedOptions: parsed.malformedOptions.sorted()
        )
    }

    var allowsWrites: Bool {
        rw?.lowercased() != "false"
    }

    func validateForMount() throws {
        if !malformedOptions.isEmpty {
            let options = malformedOptions.map { "osix.\($0)" }.joined(separator: ", ")
            throw OSIxMountOptionsValidationError(description: "malformed encoded mount option \(options)")
        }

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

        if let rw, rw.lowercased() != "true", rw.lowercased() != "false" {
            throw OSIxMountOptionsValidationError(description: "osix.rw must be true or false")
        }

        try validateDirectory(path: workspace!, option: "osix.workspace")
        try validateDirectory(path: lower!, option: "osix.lower", rejectWorldWritable: true)
        try validateDirectory(path: upper!, option: "osix.upper", rejectWorldWritable: true)
        try validateDirectory(path: work!, option: "osix.work", rejectWorldWritable: true)
        try validateRuntimeDirectoriesAreDisjoint()
        try validateSourceDigest(sourceDigest!)
    }

    private func validateDirectory(path: String, option: String, rejectWorldWritable: Bool = false) throws {
        var statBuffer = stat()
        guard lstat(path, &statBuffer) == 0 else {
            throw OSIxMountOptionsValidationError(description: "\(option) \(path) is unavailable: \(String(cString: strerror(errno)))")
        }
        guard statBuffer.st_mode & S_IFMT == S_IFDIR else {
            throw OSIxMountOptionsValidationError(description: "\(option) \(path) is not a directory")
        }
        if rejectWorldWritable, statBuffer.st_mode & mode_t(S_IWOTH) != 0 {
            throw OSIxMountOptionsValidationError(description: "refusing world-writable runtime directory \(option) \(path)")
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

    private static func parseKeyValues(_ taskOptions: [String]) -> (values: [String: String], malformedOptions: Set<String>) {
        var values: [String: String] = [:]
        var malformedOptions = Set<String>()
        var index = 0
        while index < taskOptions.count {
            let option = taskOptions[index]
            if option == "-o", index + 1 < taskOptions.count {
                merge(optionString: taskOptions[index + 1], into: &values, malformedOptions: &malformedOptions)
                index += 2
                continue
            }
            if option.hasPrefix("osix.") {
                merge(optionString: option, into: &values, malformedOptions: &malformedOptions)
            }
            index += 1
        }
        return (values, malformedOptions)
    }

    private static func merge(optionString: String, into values: inout [String: String], malformedOptions: inout Set<String>) {
        for rawPair in optionString.split(separator: ",") {
            let pair = rawPair.split(separator: "=", maxSplits: 1).map(String.init)
            guard pair.count == 2, pair[0].hasPrefix("osix.") else {
                continue
            }
            let key = String(pair[0].dropFirst("osix.".count))
            if let decoded = decodeBase64URL(pair[1]) {
                values[key] = decoded
            } else {
                malformedOptions.insert(key)
            }
        }
    }

    private static func decodeBase64URL(_ value: String) -> String? {
        let allowed = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_")
        guard !value.isEmpty, value.unicodeScalars.allSatisfy({ allowed.contains($0) }) else {
            return nil
        }
        var base64 = value
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        let remainder = base64.count % 4
        if remainder != 0 {
            base64 += String(repeating: "=", count: 4 - remainder)
        }
        guard let data = Data(base64Encoded: base64),
              let decoded = String(data: data, encoding: .utf8) else {
            return nil
        }
        return decoded
    }
}
