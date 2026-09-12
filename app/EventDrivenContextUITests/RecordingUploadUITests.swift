import XCTest

final class RecordingUploadUITests: XCTestCase {
    @MainActor
    func testLoginSelectsDefaultPrivateProject() throws {
        let fixture = try fixtureConfiguration()
        let app = try launchAndLogin(fixture, app: XCUIApplication())
        defer { logoutIfAuthenticated(app) }

        let selectedProject = app.staticTexts["selected-project"]
        XCTAssertNotEqual(selectedProject.label, "—")
        XCTAssertTrue(app.buttons["start-recording-button"].isHittable)
    }

    @MainActor
    func testDenyingMicrophoneCreatesNoCapture() throws {
        let fixture = try fixtureConfiguration()
        let app = XCUIApplication()
        app.resetAuthorizationStatus(for: .microphone)
        addUIInterruptionMonitor(withDescription: "Deny microphone permission") { alert in
            for label in ["Don’t Allow", "Don't Allow"] {
                let deny = alert.buttons[label]
                if deny.exists {
                    deny.tap()
                    return true
                }
            }
            return false
        }
        try launchAndLogin(fixture, app: app)
        defer { logoutIfAuthenticated(app) }

        let capturesBefore = app.descendants(matching: .any).matching(identifier: "capture-row").count
        app.buttons["start-recording-button"].tap()
        app.tap()

        let deniedAlert = app.alerts.firstMatch
        XCTAssertTrue(deniedAlert.waitForExistence(timeout: 15), "The denied permission did not produce an app error.")
        XCTAssertTrue(deniedAlert.staticTexts["Microphone access was not granted. You can enable it in iOS Settings."].exists)
        deniedAlert.buttons["OK"].tap()
        XCTAssertEqual(app.descendants(matching: .any).matching(identifier: "capture-row").count, capturesBefore)
        XCTAssertTrue(app.buttons["start-recording-button"].exists)
    }

    @MainActor
    func testRecordingReachesAuthenticatedServer() throws {
        let fixture = try fixtureConfiguration()
        guard ProcessInfo.processInfo.environment["EDC_UI_TEST_ALLOW_MICROPHONE"] == "1" else {
            throw XCTSkip("Set EDC_UI_TEST_ALLOW_MICROPHONE=1 explicitly; this test records from the host Mac microphone.")
        }

        let app = XCUIApplication()
        app.resetAuthorizationStatus(for: .microphone)
        addUIInterruptionMonitor(withDescription: "Allow microphone permission") { alert in
            for label in ["Allow", "OK"] {
                let allow = alert.buttons[label]
                if allow.exists {
                    allow.tap()
                    return true
                }
            }
            return false
        }
        try launchAndLogin(fixture, app: app)
        defer { logoutIfAuthenticated(app) }

        let start = app.buttons["start-recording-button"]
        XCTAssertTrue(start.waitForExistence(timeout: 10))
        start.tap()
        app.tap()

        let finish = app.buttons["finish-recording-button"]
        XCTAssertTrue(finish.waitForExistence(timeout: 15), "Recording did not start after microphone authorization.")
        Thread.sleep(forTimeInterval: 2)
        finish.tap()

        let capture = app.descendants(matching: .any).matching(identifier: "capture-row").firstMatch
        XCTAssertTrue(capture.waitForExistence(timeout: 10), "The recording was not persisted to the local queue.")
        let uploaded = expectation(
            for: NSPredicate(
                format: "value == 'synced' OR value == 'processing' OR value == 'ready' OR value == 'processing_failed'"
            ),
            evaluatedWith: capture
        )
        wait(for: [uploaded], timeout: 90)

        XCTAssertTrue(
            app.staticTexts["server-file-event"].firstMatch.waitForExistence(timeout: 30),
            "The authenticated server record did not appear after upload."
        )
    }

    private struct Fixture {
        let endpoint: String
        let username: String
        let password: String
    }

    private enum UITestError: LocalizedError {
        case endpointMustUseHTTPS
        case step(String)

        var errorDescription: String? {
            switch self {
            case .endpointMustUseHTTPS: return "EDC_UI_TEST_ENDPOINT must use HTTPS."
            case .step(let message): return message
            }
        }
    }

    private func fixtureConfiguration() throws -> Fixture {
        let environment = ProcessInfo.processInfo.environment
        guard let endpoint = environment["EDC_UI_TEST_ENDPOINT"], !endpoint.isEmpty,
              let username = environment["EDC_UI_TEST_USERNAME"], !username.isEmpty,
              let password = environment["EDC_UI_TEST_PASSWORD"], !password.isEmpty else {
            throw XCTSkip("Set the EDC UI test fixture variables in the temporary xctestrun file.")
        }
        guard URL(string: endpoint)?.scheme?.lowercased() == "https" else {
            throw UITestError.endpointMustUseHTTPS
        }
        return Fixture(endpoint: endpoint, username: username, password: password)
    }

    @MainActor
    @discardableResult
    private func launchAndLogin(_ fixture: Fixture, app: XCUIApplication) throws -> XCUIApplication {
        addUIInterruptionMonitor(withDescription: "Decline password save") { alert in
            let notNow = alert.buttons["Not Now"]
            guard notNow.exists else { return false }
            notNow.tap()
            return true
        }
        app.launchArguments += ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launch()

        let endpointField = app.textFields["endpoint-field"]
        if !endpointField.waitForExistence(timeout: 3) {
            logoutIfAuthenticated(app)
        }
        guard endpointField.waitForExistence(timeout: 10) else {
            throw diagnosed("The app did not reach the login screen.", app: app)
        }
        try replaceText(in: endpointField, with: fixture.endpoint, verifyValue: true, app: app)
        try replaceText(in: app.textFields["username-field"], with: fixture.username, verifyValue: true, app: app)
        try replaceText(in: app.secureTextFields["password-field"], with: fixture.password, verifyValue: false, app: app)
        let login = app.buttons["login-button"]
        guard login.waitForExistence(timeout: 5) else {
            throw diagnosed("The login button did not appear after entering fixture credentials.", app: app)
        }
        login.tap()

        let selectedProject = app.staticTexts["selected-project"]
        guard selectedProject.waitForExistence(timeout: 30) else {
            throw diagnosed("Login did not reach the records screen.", app: app)
        }
        let selected = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "label != '—'"),
            object: selectedProject
        )
        guard XCTWaiter.wait(for: [selected], timeout: 30) == .completed else {
            throw diagnosed("The default private project was not selected.", app: app)
        }
        let start = app.buttons["start-recording-button"]
        guard start.waitForExistence(timeout: 5) else {
            throw diagnosed("The recording action did not appear after login.", app: app)
        }
        if !start.isHittable {
            app.tap()
        }
        let ready = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "enabled == true AND hittable == true"),
            object: start
        )
        guard XCTWaiter.wait(for: [ready], timeout: 15) == .completed else {
            throw diagnosed("The recording action remained blocked after login initialization.", app: app)
        }
        return app
    }

    @MainActor
    private func logoutIfAuthenticated(_ app: XCUIApplication) {
        guard app.tabBars.buttons.count >= 3 else { return }
        app.tabBars.buttons.element(boundBy: 2).tap()
        let logout = app.buttons["logout-button"]
        guard logout.waitForExistence(timeout: 3) else { return }
        logout.tap()
        _ = app.textFields["endpoint-field"].waitForExistence(timeout: 5)
    }

    @MainActor
    private func replaceText(
        in element: XCUIElement,
        with replacement: String,
        verifyValue: Bool,
        app: XCUIApplication
    ) throws {
        guard element.waitForExistence(timeout: 10), element.isHittable else {
            throw diagnosed("A required login field is not hittable.", app: app)
        }
        element.tap()
        let current = element.value as? String ?? ""
        if !current.isEmpty, current != element.placeholderValue {
            element.press(forDuration: 1)
            let selectAll = app.menuItems["Select All"]
            let selectAllButton = app.buttons["Select All"]
            if selectAll.waitForExistence(timeout: 3) {
                selectAll.tap()
            } else if selectAllButton.waitForExistence(timeout: 1) {
                selectAllButton.tap()
            } else {
                throw diagnosed("The system Select All action did not appear for a populated login field.", app: app)
            }
            element.typeKey(XCUIKeyboardKey.delete.rawValue, modifierFlags: [])
        }
        element.typeText(replacement)
        if verifyValue, element.value as? String != replacement {
            throw diagnosed("A login field did not contain the requested fixture value after replacement.", app: app)
        }
    }

    @MainActor
    private func diagnosed(_ message: String, app: XCUIApplication) -> UITestError {
        let screenshot = XCTAttachment(screenshot: app.screenshot())
        screenshot.name = "UI at failure"
        screenshot.lifetime = .keepAlways
        add(screenshot)
        let hierarchy = XCTAttachment(string: app.debugDescription)
        hierarchy.name = "Accessibility hierarchy at failure"
        hierarchy.lifetime = .keepAlways
        add(hierarchy)
        return .step(message)
    }
}
