// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "Pulse",
    platforms: [.iOS(.v14), .macOS(.v11), .tvOS(.v14)],
    products: [
        .library(name: "Pulse", targets: ["Pulse"]),
        .library(name: "PulseCore", targets: ["PulseCore"]),
    ],
    targets: [
        // Foundation only: builds and tests on Linux too.
        .target(name: "PulseCore"),
        // UIKit / UserNotifications parts, compiled only where those frameworks exist.
        .target(name: "Pulse", dependencies: ["PulseCore"]),
        .testTarget(name: "PulseCoreTests", dependencies: ["PulseCore"]),
    ]
)
