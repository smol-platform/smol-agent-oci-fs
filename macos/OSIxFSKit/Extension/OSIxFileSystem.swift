import Foundation
import FSKit

@objc
final class OSIxFileSystem: FSUnaryFileSystem, FSUnaryFileSystemOperations {
    func probeResource(resource: FSResource, replyHandler: @escaping (FSProbeResult?, (any Error)?) -> Void) {
        replyHandler(.usable(name: "OSIxFS", containerID: FSContainerIdentifier(uuid: UUID())), nil)
    }

    func loadResource(resource: FSResource, options: FSTaskOptions, replyHandler: @escaping (FSVolume?, (any Error)?) -> Void) {
        let volume = OSIxVolume(
            volumeID: FSVolume.Identifier(uuid: UUID()),
            volumeName: FSFileName(string: "OSIx")
        )
        replyHandler(volume, nil)
    }

    func unloadResource(resource: FSResource, options: FSTaskOptions) async throws {
    }
}
