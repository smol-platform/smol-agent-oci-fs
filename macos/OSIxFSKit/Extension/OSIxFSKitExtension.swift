import ExtensionFoundation
import Foundation
import FSKit

@main
struct OSIxFSKitExtension: UnaryFileSystemExtension {
    var fileSystem: FSUnaryFileSystem & FSUnaryFileSystemOperations {
        OSIxFileSystem()
    }
}
