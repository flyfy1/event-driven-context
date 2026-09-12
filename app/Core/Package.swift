// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "EventDrivenContextCore",
    platforms: [.macOS(.v13)],
    products: [
        .library(name: "EventDrivenContextCore", targets: ["EventDrivenContextCore"])
    ],
    targets: [
        .target(name: "EventDrivenContextCore"),
        .testTarget(name: "EventDrivenContextCoreTests", dependencies: ["EventDrivenContextCore"])
    ]
)
