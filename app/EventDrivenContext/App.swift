import SwiftUI

@main
struct EventDrivenContextApp: App {
    @StateObject private var model = AppModel()
    @AppStorage("app_locale") private var locale = "en"

    init() {
        if UserDefaults.standard.string(forKey: "app_locale") == nil {
            let preferred = Locale.preferredLanguages.first?.lowercased() ?? "en"
            let selected: String
            if preferred.hasPrefix("zh") { selected = "zh-Hans" }
            else if preferred.hasPrefix("ms") { selected = "ms" }
            else if preferred.hasPrefix("hi") { selected = "hi" }
            else { selected = "en" }
            UserDefaults.standard.set(selected, forKey: "app_locale")
        }
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(model)
                .environment(\.locale, Locale(identifier: locale))
        }
    }
}
