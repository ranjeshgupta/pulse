import Foundation

/// A JSON value. The SDK keeps events, stored state and server answers in this form.
public enum JSON: Equatable, Codable {
    case null
    case bool(Bool)
    case int(Int64)
    case double(Double)
    case string(String)
    case array([JSON])
    case object([String: JSON])

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() {
            self = .null
        } else if let b = try? c.decode(Bool.self) {
            self = .bool(b)
        } else if let i = try? c.decode(Int64.self) {
            self = .int(i)
        } else if let d = try? c.decode(Double.self) {
            self = .double(d)
        } else if let s = try? c.decode(String.self) {
            self = .string(s)
        } else if let a = try? c.decode([JSON].self) {
            self = .array(a)
        } else {
            self = .object(try c.decode([String: JSON].self))
        }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case .bool(let b): try c.encode(b)
        case .int(let i): try c.encode(i)
        case .double(let d):
            if d.isFinite { try c.encode(d) } else { try c.encodeNil() }
        case .string(let s): try c.encode(s)
        case .array(let a): try c.encode(a)
        case .object(let o): try c.encode(o)
        }
    }

    /// Converts a Swift value to JSON: strings, numbers, booleans, dates (ISO 8601 UTC), arrays,
    /// dictionaries with string keys, nil / NSNull; anything else becomes its description.
    public init(any value: Any?) {
        guard let value = value else { self = .null; return }
        let mirror = Mirror(reflecting: value)
        if mirror.displayStyle == .optional {
            if let child = mirror.children.first { self = JSON(any: child.value) } else { self = .null }
            return
        }
        switch value {
        case let v as JSON: self = v
        case is NSNull: self = .null
        case let v as String: self = .string(v)
        case let v as Bool: self = .bool(v)
        case let v as Int: self = .int(Int64(v))
        case let v as Int8: self = .int(Int64(v))
        case let v as Int16: self = .int(Int64(v))
        case let v as Int32: self = .int(Int64(v))
        case let v as Int64: self = .int(v)
        case let v as UInt: self = v <= UInt(Int64.max) ? .int(Int64(v)) : .double(Double(v))
        case let v as UInt8: self = .int(Int64(v))
        case let v as UInt16: self = .int(Int64(v))
        case let v as UInt32: self = .int(Int64(v))
        case let v as Double: self = v.isFinite ? .double(v) : .null
        case let v as Float: self = v.isFinite ? .double(Double(v)) : .null
        case let v as Date: self = .string(PulseTime.iso(v))
        case let v as URL: self = .string(v.absoluteString)
        case let v as [String: Any]: self = .object(v.mapValues { JSON(any: $0) })
        case let v as [AnyHashable: Any]:
            var o: [String: JSON] = [:]
            for (k, x) in v { o["\(k.base)"] = JSON(any: x) }
            self = .object(o)
        case let v as [Any]: self = .array(v.map { JSON(any: $0) })
        case let v as NSNumber: self = .double(v.doubleValue)
        default: self = .string(String(describing: value))
        }
    }

    /// Encodes as compact JSON.
    public func encoded() -> Data {
        let enc = JSONEncoder()
        if #available(macOS 10.13, iOS 11.0, tvOS 11.0, *) { enc.outputFormatting = [.sortedKeys] }
        return (try? enc.encode(self)) ?? Data("null".utf8)
    }

    public var encodedString: String { String(decoding: encoded(), as: UTF8.self) }

    /// Parses [data]; nil when it is not JSON.
    public static func decode(_ data: Data) -> JSON? {
        try? JSONDecoder().decode(JSON.self, from: data)
    }

    public static func decode(_ text: String) -> JSON? { decode(Data(text.utf8)) }

    public subscript(key: String) -> JSON? {
        if case .object(let o) = self { return o[key] }
        return nil
    }

    public var string: String? { if case .string(let s) = self { return s }; return nil }

    /// A non-empty string.
    public var text: String? { if case .string(let s) = self, !s.isEmpty { return s }; return nil }

    public var int: Int64? {
        switch self {
        case .int(let i): return i
        case .double(let d) where d == d.rounded() && abs(d) < 9e15: return Int64(d)
        default: return nil
        }
    }

    public var double: Double? {
        switch self {
        case .int(let i): return Double(i)
        case .double(let d): return d
        default: return nil
        }
    }

    public var bool: Bool? { if case .bool(let b) = self { return b }; return nil }
    public var array: [JSON]? { if case .array(let a) = self { return a }; return nil }
    public var object: [String: JSON]? { if case .object(let o) = self { return o }; return nil }

    /// The value as a plain Swift value (`String`, `Int64`, `Double`, `Bool`, `[Any]`, `[String: Any]`, `NSNull`).
    public var value: Any {
        switch self {
        case .null: return NSNull()
        case .bool(let b): return b
        case .int(let i): return i
        case .double(let d): return d
        case .string(let s): return s
        case .array(let a): return a.map { $0.value }
        case .object(let o): return o.mapValues { $0.value }
        }
    }
}

/// Time formatting the server understands.
public enum PulseTime {
    /// `2024-05-01T10:00:00.000Z`.
    public static func iso(_ date: Date) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "UTC")
        f.dateFormat = "yyyy-MM-dd'T'HH:mm:ss.SSS'Z'"
        return f.string(from: date)
    }

    /// Parses an RFC 3339 time (`2024-05-01T10:00:00.123456Z`, `...+05:30`), or returns nil.
    public static func parse(_ s: String?) -> Date? {
        guard var s = s, s.count >= 20 else { return nil }
        var fraction = 0.0
        if let dot = s.firstIndex(of: "."), s.distance(from: s.startIndex, to: dot) == 19 {
            var end = s.index(after: dot)
            while end < s.endIndex, s[end].isNumber { end = s.index(after: end) }
            let digits = String(s[s.index(after: dot)..<end])
            guard !digits.isEmpty else { return nil }
            fraction = Double("0." + digits) ?? 0
            s.removeSubrange(dot..<end)
        }
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "UTC")
        f.dateFormat = "yyyy-MM-dd'T'HH:mm:ssXXXXX"
        guard let d = f.date(from: s) else { return nil }
        return d.addingTimeInterval(fraction)
    }

    /// `2024-05-01` in the device's time zone.
    static func day(_ date: Date) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd"
        return f.string(from: date)
    }
}
