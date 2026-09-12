import CryptoKit
import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

struct APIUser: Codable, Equatable {
    let id: String
    let username: String
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id, username
        case createdAt = "created_at"
    }
}

struct LoginResponse: Codable {
    let user: APIUser
    let token: String
    let expiresAt: Date

    enum CodingKeys: String, CodingKey {
        case user, token
        case expiresAt = "expires_at"
    }
}

struct APIProject: Codable, Identifiable, Equatable {
    let id: String
    let name: String
    let description: String
    let ownerUserID: String
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id, name, description
        case ownerUserID = "owner_user_id"
        case createdAt = "created_at"
    }
}

struct APIFileInfo: Codable, Equatable {
    let id: String
    let filename: String
    let mediaType: String
    let sizeBytes: Int
    let sha256: String

    enum CodingKeys: String, CodingKey {
        case id, filename, sha256
        case mediaType = "media_type"
        case sizeBytes = "size_bytes"
    }
}

struct APIContent: Codable, Equatable {
    let kind: String
    let text: String?
    let file: APIFileInfo?
}

struct APIProvenance: Codable, Equatable {
    let kind: String
    let sourceEventIDs: [String]?

    enum CodingKeys: String, CodingKey {
        case kind
        case sourceEventIDs = "source_event_ids"
    }
}

struct APIEvent: Codable, Identifiable, Equatable {
    let id: String
    let projectID: String
    let actorUserID: String
    let actorUsername: String
    let recordedAt: String
    let occurredAt: String?
    let content: APIContent
    let provenance: APIProvenance

    enum CodingKeys: String, CodingKey {
        case id, content, provenance
        case projectID = "project_id"
        case actorUserID = "actor_user_id"
        case actorUsername = "actor_username"
        case recordedAt = "recorded_at"
        case occurredAt = "occurred_at"
    }
}

struct APIAutomationRunInput: Codable, Equatable {
    let eventID: String
    let fileID: String?

    enum CodingKeys: String, CodingKey {
        case eventID = "event_id"
        case fileID = "file_id"
    }
}

struct APIAutomationRun: Codable, Identifiable, Equatable {
    let id: String
    let installationID: String
    let projectID: String
    let skillID: String
    let generation: Int
    let inputs: [APIAutomationRunInput]
    let status: String
    let attemptCount: Int
    let nextAttemptAt: String?
    let outputEventIDs: [String]?
    let outputEventID: String?
    let failureKind: String?
    let failureCode: String?
    let noOutputReason: String?
    let createdAt: String
    let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case id, inputs, status, generation
        case installationID = "installation_id"
        case projectID = "project_id"
        case skillID = "skill_id"
        case attemptCount = "attempt_count"
        case nextAttemptAt = "next_attempt_at"
        case outputEventIDs = "output_event_ids"
        case outputEventID = "output_event_id"
        case failureKind = "failure_kind"
        case failureCode = "failure_code"
        case noOutputReason = "no_output_reason"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}

struct APIInboxCandidateItem: Codable, Equatable {
    let kind: String
    let text: String
    let sourceEventIDs: [String]

    enum CodingKeys: String, CodingKey {
        case kind, text
        case sourceEventIDs = "source_event_ids"
    }
}

struct APIInboxCandidate: Codable, Equatable {
    let schemaVersion: Int
    let outcome: String
    let outputSlot: String
    let kind: String
    let text: String
    let sourceEventIDs: [String]
    let items: [APIInboxCandidateItem]?

    enum CodingKeys: String, CodingKey {
        case outcome, kind, text, items
        case schemaVersion = "schema_version"
        case outputSlot = "output_slot"
        case sourceEventIDs = "source_event_ids"
    }
}

struct APIInboxEntry: Codable, Identifiable, Equatable {
    let id: String
    let recipientUserID: String
    let projectID: String
    let installationID: String
    let runID: String
    let outputEventID: String?
    let outputEventIDs: [String]?
    let candidateDigest: String
    let candidate: APIInboxCandidate
    let createdAt: String
    let readAt: String?

    enum CodingKeys: String, CodingKey {
        case id, candidate
        case recipientUserID = "recipient_user_id"
        case projectID = "project_id"
        case installationID = "installation_id"
        case runID = "run_id"
        case outputEventID = "output_event_id"
        case outputEventIDs = "output_event_ids"
        case candidateDigest = "candidate_digest"
        case createdAt = "created_at"
        case readAt = "read_at"
    }
}

struct SessionCredential: Codable, Equatable {
    let endpoint: String
    let user: APIUser
    let token: String
    let expiresAt: Date
}

enum APIClientError: Error {
    case invalidEndpoint
    case transport
    case server(status: Int, code: String)
    case invalidResponse

    var localizationKey: String {
        switch self {
        case .invalidEndpoint: return "error_https_required"
        case .transport: return "error_network"
        case .invalidResponse: return "error_invalid_response"
        case .server(_, let code):
            switch code {
            case "unauthenticated": return "error_session_expired"
            case "forbidden": return "error_project_access"
            case "conflict": return "error_conflict"
            case "request_too_large", "too_large": return "error_file_too_large"
            case "rate_limited": return "error_rate_limited"
            default: return "error_server"
            }
        }
    }
}

struct APIClient {
    private struct ProjectsResponse: Decodable { let projects: [APIProject] }
    private struct MembersResponse: Decodable { let members: [APIUser] }
    private struct EventsResponse: Decodable {
        let events: [APIEvent]
        let nextCursor: String?
        enum CodingKeys: String, CodingKey { case events; case nextCursor = "next_cursor" }
    }
    private struct InboxResponse: Decodable { let entries: [APIInboxEntry] }
    private struct RunsResponse: Decodable { let runs: [APIAutomationRun] }
    private struct ErrorEnvelope: Decodable {
        struct Payload: Decodable { let code: String }
        let error: Payload
    }

    let baseURL: URL
    let token: String?
    private let session: URLSession
    private let decoder: JSONDecoder

    init(endpoint: String, token: String? = nil, session: URLSession = .shared) throws {
        guard let url = URL(string: endpoint),
              url.scheme?.lowercased() == "https",
              url.host != nil,
              url.user == nil,
              url.password == nil,
              url.query == nil,
              url.fragment == nil,
              url.path.isEmpty || url.path == "/" else {
            throw APIClientError.invalidEndpoint
        }
        self.baseURL = url
        self.token = token
        self.session = session
        self.decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
    }

    func register(username: String, password: String) async throws -> APIUser {
        try await json("v1/auth/register", method: "POST", body: ["username": username, "password": password])
    }

    func login(username: String, password: String) async throws -> LoginResponse {
        try await json("v1/auth/login", method: "POST", body: ["username": username, "password": password])
    }

    func logout() async throws {
        let _: EmptyResponse = try await json("v1/auth/logout", method: "POST", body: Optional<String>.none)
    }

    func projects() async throws -> [APIProject] {
        let response: ProjectsResponse = try await json("v1/projects", method: "GET", body: Optional<String>.none)
        return response.projects
    }

    func createProject(name: String, description: String) async throws -> APIProject {
        try await json("v1/projects", method: "POST", body: ["name": name, "description": description])
    }

    func members(projectID: String) async throws -> [APIUser] {
        let response: MembersResponse = try await json(
            "v1/members/query",
            method: "POST",
            body: ["project_id": projectID]
        )
        return response.members
    }

    func events(projectID: String) async throws -> [APIEvent] {
        var cursor: String?
        var collected: [APIEvent] = []
        repeat {
            let response: EventsResponse = try await json(
                "v1/events/query",
                method: "POST",
                body: QueryBody(projectID: projectID, limit: 100, cursor: cursor)
            )
            collected.append(contentsOf: response.events)
            cursor = response.nextCursor.flatMap { $0.isEmpty ? nil : $0 }
        } while cursor != nil
        return collected.sorted { $0.recordedAt > $1.recordedAt }
    }

    func event(id: String) async throws -> APIEvent {
        let safeID = id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? id
        return try await json("v1/events/\(safeID)", method: "GET", body: Optional<String>.none)
    }

    func inbox() async throws -> [APIInboxEntry] {
        let response: InboxResponse = try await json("v1/inbox", method: "GET", body: Optional<String>.none)
        return response.entries.sorted { $0.createdAt > $1.createdAt }
    }

    func markInboxRead(entryID: String) async throws -> APIInboxEntry {
        let safeID = entryID.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? entryID
        return try await json("v1/inbox/\(safeID)/read", method: "POST", body: Optional<String>.none)
    }

    func automationRuns(projectID: String) async throws -> [APIAutomationRun] {
        let response: RunsResponse = try await json(
            "v1/automation/runs",
            method: "GET",
            queryItems: [URLQueryItem(name: "project_id", value: projectID)],
            body: Optional<String>.none
        )
        return response.runs.sorted { $0.updatedAt > $1.updatedAt }
    }

    func upload(_ item: CaptureItem) async throws -> APIEvent {
        guard let metadata = item.metadataJSON.data(using: .utf8),
              (try? JSONSerialization.jsonObject(with: metadata)) != nil else {
            throw APIClientError.invalidResponse
        }
        let fileURL = URL(fileURLWithPath: item.filePath)
        let fileData: Data
        do { fileData = try Data(contentsOf: fileURL) }
        catch { throw APIClientError.invalidResponse }
        let digest = SHA256.hash(data: fileData).map { String(format: "%02x", $0) }.joined()
        guard Int64(fileData.count) == item.sizeBytes, !item.sha256.isEmpty,
              digest.caseInsensitiveCompare(item.sha256) == .orderedSame else {
            throw APIClientError.server(status: 0, code: "local_file_changed")
        }

        let boundary = "Boundary-\(UUID().uuidString)"
        var body = Data()
        body.appendFormField(name: "project_id", value: item.projectID, boundary: boundary)
        body.appendFormField(name: "metadata", value: item.metadataJSON, boundary: boundary)
        body.appendFormField(name: "occurred_at", value: item.occurredAt, boundary: boundary)
        body.appendFormField(name: "idempotency_key", value: item.captureID, boundary: boundary)
        body.appendFile(
            name: "file",
            filename: fileURL.lastPathComponent,
            mediaType: item.mediaType,
            data: fileData,
            boundary: boundary
        )
        body.append("--\(boundary)--\r\n".data(using: .utf8)!)

        var request = try makeRequest(path: "v1/media-events", method: "POST")
        request.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")
        request.timeoutInterval = 190
        request.httpBody = body
        return try await send(request)
    }

    func download(fileID: String) async throws -> Data {
        var request = try makeRequest(
            path: "v1/files/\(fileID.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? fileID)/content",
            method: "GET"
        )
        request.setValue("*/*", forHTTPHeaderField: "Accept")
        let (data, response): (Data, URLResponse)
        do { (data, response) = try await session.data(for: request) }
        catch { throw APIClientError.transport }
        try check(response: response, data: data)
        return data
    }

    private func json<Body: Encodable, Output: Decodable>(
        _ path: String,
        method: String,
        queryItems: [URLQueryItem] = [],
        body: Body?
    ) async throws -> Output {
        var request = try makeRequest(path: path, method: method, queryItems: queryItems)
        if let body {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONEncoder().encode(body)
        }
        return try await send(request)
    }

    private func send<Output: Decodable>(_ request: URLRequest) async throws -> Output {
        let (data, response): (Data, URLResponse)
        do { (data, response) = try await session.data(for: request) }
        catch { throw APIClientError.transport }
        try check(response: response, data: data)
        do { return try decoder.decode(Output.self, from: data) }
        catch { throw APIClientError.invalidResponse }
    }

    private func check(response: URLResponse, data: Data) throws {
        guard let http = response as? HTTPURLResponse else { throw APIClientError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            let code = (try? decoder.decode(ErrorEnvelope.self, from: data).error.code) ?? "unknown"
            throw APIClientError.server(status: http.statusCode, code: code)
        }
    }

    private func makeRequest(path: String, method: String, queryItems: [URLQueryItem] = []) throws -> URLRequest {
        let pathURL = baseURL.appendingPathComponent(path)
        guard var components = URLComponents(url: pathURL, resolvingAgainstBaseURL: false) else {
            throw APIClientError.invalidEndpoint
        }
        components.queryItems = queryItems.isEmpty ? nil : queryItems
        guard let url = components.url else { throw APIClientError.invalidEndpoint }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.timeoutInterval = 60
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        return request
    }
}

private struct QueryBody: Encodable {
    let projectID: String
    let limit: Int
    let cursor: String?
    enum CodingKeys: String, CodingKey { case projectID = "project_id", limit, cursor }
}

private struct EmptyResponse: Decodable {}

private extension Data {
    mutating func appendFormField(name: String, value: String, boundary: String) {
        append("--\(boundary)\r\n".data(using: .utf8)!)
        append("Content-Disposition: form-data; name=\"\(name)\"\r\n\r\n".data(using: .utf8)!)
        append(value.data(using: .utf8)!)
        append("\r\n".data(using: .utf8)!)
    }

    mutating func appendFile(name: String, filename: String, mediaType: String, data: Data, boundary: String) {
        let safeFilename = filename.replacingOccurrences(of: "\"", with: "_")
        append("--\(boundary)\r\n".data(using: .utf8)!)
        append("Content-Disposition: form-data; name=\"\(name)\"; filename=\"\(safeFilename)\"\r\n".data(using: .utf8)!)
        append("Content-Type: \(mediaType)\r\n\r\n".data(using: .utf8)!)
        append(data)
        append("\r\n".data(using: .utf8)!)
    }
}
