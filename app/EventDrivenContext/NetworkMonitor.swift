import Foundation
import Network

final class NetworkMonitor {
    private let monitor = NWPathMonitor()
    private let queue = DispatchQueue(label: "life.integ.context.network")
    private let lock = NSLock()
    private var reachable = false
    var isReachable: Bool { lock.withLock { reachable } }
    var onReachable: (() -> Void)?

    init() {
        monitor.pathUpdateHandler = { [weak self] path in
            guard let self else { return }
            let reachable = path.status == .satisfied
            self.lock.withLock { self.reachable = reachable }
            if reachable { DispatchQueue.main.async { self.onReachable?() } }
        }
        monitor.start(queue: queue)
    }

    deinit { monitor.cancel() }
}
