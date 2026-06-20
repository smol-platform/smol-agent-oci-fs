import Foundation

@main
struct DirtyIndexSmoke {
    static func main() throws {
        guard CommandLine.arguments.count == 2 || CommandLine.arguments.count == 4 else {
            fputs("usage: DirtyIndexSmoke UPPER_DIR [WORKSPACE_ROOT SOURCE_DIGEST]\n", stderr)
            Foundation.exit(64)
        }
        let parentTree: [String: OSIxTreeEntry]
        if CommandLine.arguments.count == 4 {
            parentTree = OSIxDirtyIndex.parentTree(workspace: CommandLine.arguments[2], sourceDigest: CommandLine.arguments[3])
        } else {
            parentTree = [:]
        }
        let index = OSIxDirtyIndex.rebuild(upper: CommandLine.arguments[1], parentTree: parentTree)
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        FileHandle.standardOutput.write(try encoder.encode(Output(dirtyBytes: index.dirtyBytes, paths: index.paths)))
        FileHandle.standardOutput.write(Data("\n".utf8))
    }

    struct Output: Encodable {
        let dirtyBytes: Int64
        let paths: [String: String]
    }
}
