// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "OSIxFSKit",
    platforms: [
        .macOS("15.4")
    ],
    products: [
        .executable(name: "osix-fskitctl", targets: ["osix-fskitctl"])
    ],
    targets: [
        .executableTarget(
            name: "osix-fskitctl",
            path: "Sources/osix-fskitctl"
        )
    ]
)
