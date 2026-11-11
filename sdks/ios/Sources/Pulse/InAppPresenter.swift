import Foundation
import PulseCore

#if canImport(UIKit) && !os(watchOS)
import UIKit

/// Shows in-app messages with UIKit: `modal` (a card over a dimmed background) and `fullscreen` as a
/// presented view controller over the top one, `banner` as a view on the key window (top or bottom,
/// hides itself after 8 s, a tap follows its first url button).
final class UIKitInAppPresenter: PulseInAppPresenter {
    private let lock = NSLock()
    private var _active = false

    /// Whether the app is active (kept up to date from the main thread).
    var active: Bool {
        get { lock.lock(); defer { lock.unlock() }; return _active }
        set { lock.lock(); _active = newValue; lock.unlock() }
    }

    func canPresent() -> Bool { active }

    func present(_ message: PulseInAppMessage, completion: @escaping (PulseInAppOutcome) -> Void) {
        Task { @MainActor in
            var called = false
            let done: (PulseInAppOutcome) -> Void = { outcome in
                if called { return }
                called = true
                completion(outcome)
            }
            guard let window = Self.keyWindow() else { return done(PulseInAppOutcome()) }
            if message.layout == "banner" {
                InAppBanner.show(message, in: window, done: done)
                return
            }
            guard let top = Self.topViewController(window.rootViewController) else { return done(PulseInAppOutcome()) }
            let vc = InAppViewController(message: message, done: done)
            top.present(vc, animated: true)
        }
    }

    @MainActor
    static func keyWindow() -> UIWindow? {
        let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
        let windows = scenes.filter { $0.activationState == .foregroundActive }.flatMap { $0.windows } + scenes.flatMap { $0.windows }
        return windows.first { $0.isKeyWindow } ?? windows.first
    }

    @MainActor
    static func topViewController(_ root: UIViewController?) -> UIViewController? {
        var top = root
        while let presented = top?.presentedViewController, !presented.isBeingDismissed { top = presented }
        if let nav = top as? UINavigationController { return nav.visibleViewController ?? nav }
        if let tab = top as? UITabBarController { return tab.selectedViewController ?? tab }
        return top
    }
}

@MainActor
enum InAppStyle {
    static func color(_ hex: String?, _ fallback: UIColor) -> UIColor {
        guard var h = hex?.trimmingCharacters(in: .whitespaces), h.hasPrefix("#") else { return fallback }
        h.removeFirst()
        guard h.count == 6, let v = UInt32(h, radix: 16) else { return fallback }
        return UIColor(red: CGFloat((v >> 16) & 0xff) / 255, green: CGFloat((v >> 8) & 0xff) / 255,
                       blue: CGFloat(v & 0xff) / 255, alpha: 1)
    }

    static func loadImage(_ url: String?, into view: UIImageView) {
        guard let s = url, let u = URL(string: s), u.scheme == "https" || u.scheme == "http" else { return }
        URLSession.shared.dataTask(with: u) { data, _, _ in
            guard let data = data else { return }
            Task { @MainActor in
                if let img = UIImage(data: data) {
                    view.image = img
                    view.isHidden = false
                }
            }
        }.resume()
    }

    /// Image, title, body and (optionally) buttons in a vertical stack.
    static func stack(_ m: PulseInAppMessage, text: UIColor, accent: UIColor, buttons: Bool,
                      tap: @escaping (PulseInAppButton) -> Void) -> UIStackView {
        let c = m.content
        let stack = UIStackView()
        stack.axis = .vertical
        stack.spacing = 8
        if c.imageURL != nil {
            let image = UIImageView()
            image.contentMode = .scaleAspectFit
            image.isHidden = true
            image.heightAnchor.constraint(lessThanOrEqualToConstant: 220).isActive = true
            loadImage(c.imageURL, into: image)
            stack.addArrangedSubview(image)
        }
        if !c.title.isEmpty {
            let l = UILabel()
            l.text = c.title
            l.numberOfLines = 0
            l.textColor = text
            l.font = .boldSystemFont(ofSize: m.layout == "banner" ? 16 : 20)
            stack.addArrangedSubview(l)
        }
        if !c.body.isEmpty {
            let l = UILabel()
            l.text = c.body
            l.numberOfLines = 0
            l.textColor = text
            l.font = .systemFont(ofSize: m.layout == "banner" ? 14 : 16)
            stack.addArrangedSubview(l)
        }
        if buttons && !c.buttons.isEmpty {
            let row = UIStackView()
            row.axis = .horizontal
            row.spacing = 8
            row.distribution = .fillEqually
            for (i, b) in c.buttons.enumerated() {
                let button = UIButton(type: .system)
                button.setTitle(b.text, for: .normal)
                button.layer.cornerRadius = 6
                button.contentEdgeInsets = UIEdgeInsets(top: 10, left: 14, bottom: 10, right: 14)
                if i == 0 {
                    button.backgroundColor = accent
                    button.setTitleColor(.white, for: .normal)
                } else {
                    button.layer.borderWidth = 1
                    button.layer.borderColor = text.cgColor
                    button.setTitleColor(text, for: .normal)
                }
                button.addAction(UIAction { _ in tap(b) }, for: .touchUpInside)
                row.addArrangedSubview(button)
            }
            stack.setCustomSpacing(16, after: stack.arrangedSubviews.last ?? row)
            stack.addArrangedSubview(row)
        }
        return stack
    }
}

/// Modal and full-screen in-app messages.
final class InAppViewController: UIViewController {
    private let message: PulseInAppMessage
    private let done: (PulseInAppOutcome) -> Void
    private var outcome = PulseInAppOutcome()

    init(message: PulseInAppMessage, done: @escaping (PulseInAppOutcome) -> Void) {
        self.message = message
        self.done = done
        super.init(nibName: nil, bundle: nil)
        modalPresentationStyle = .overFullScreen
        modalTransitionStyle = .crossDissolve
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    override func viewDidLoad() {
        super.viewDidLoad()
        let c = message.content
        let bg = InAppStyle.color(c.background, .systemBackground)
        let fg = InAppStyle.color(c.textColor, .label)
        let accent = InAppStyle.color(c.buttonColor, view.tintColor)
        let full = message.layout == "fullscreen"

        view.backgroundColor = full ? bg : UIColor.black.withAlphaComponent(0.6)
        if !full {
            let outside = UITapGestureRecognizer(target: self, action: #selector(tapOutside(_:)))
            outside.cancelsTouchesInView = false
            view.addGestureRecognizer(outside)
        }

        let card = UIView()
        card.backgroundColor = bg
        card.layer.cornerRadius = full ? 0 : 12
        card.translatesAutoresizingMaskIntoConstraints = false
        card.tag = 1
        view.addSubview(card)

        let stack = InAppStyle.stack(message, text: fg, accent: accent, buttons: true) { [weak self] b in
            self?.close(PulseInAppOutcome(button: b.isURL ? b : nil))
        }
        stack.translatesAutoresizingMaskIntoConstraints = false
        card.addSubview(stack)

        let closeButton = UIButton(type: .system)
        closeButton.setTitle("\u{00D7}", for: .normal)
        closeButton.titleLabel?.font = .systemFont(ofSize: 26)
        closeButton.setTitleColor(fg, for: .normal)
        closeButton.accessibilityLabel = "Close"
        closeButton.translatesAutoresizingMaskIntoConstraints = false
        closeButton.addAction(UIAction { [weak self] _ in self?.close(PulseInAppOutcome()) }, for: .touchUpInside)
        card.addSubview(closeButton)

        let g = view.safeAreaLayoutGuide
        var constraints = [
            closeButton.topAnchor.constraint(equalTo: card.topAnchor, constant: 8),
            closeButton.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -8),
            stack.topAnchor.constraint(equalTo: closeButton.bottomAnchor, constant: 4),
            stack.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 20),
            stack.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -20),
            stack.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -20),
        ]
        if full {
            constraints += [
                card.topAnchor.constraint(equalTo: g.topAnchor),
                card.leadingAnchor.constraint(equalTo: g.leadingAnchor),
                card.trailingAnchor.constraint(equalTo: g.trailingAnchor),
                card.bottomAnchor.constraint(lessThanOrEqualTo: g.bottomAnchor),
            ]
        } else {
            constraints += [
                card.centerXAnchor.constraint(equalTo: g.centerXAnchor),
                card.centerYAnchor.constraint(equalTo: g.centerYAnchor),
                card.widthAnchor.constraint(lessThanOrEqualToConstant: 420),
                card.leadingAnchor.constraint(greaterThanOrEqualTo: g.leadingAnchor, constant: 24),
                card.trailingAnchor.constraint(lessThanOrEqualTo: g.trailingAnchor, constant: -24),
                card.topAnchor.constraint(greaterThanOrEqualTo: g.topAnchor, constant: 24),
                card.bottomAnchor.constraint(lessThanOrEqualTo: g.bottomAnchor, constant: -24),
            ]
            let wide = card.widthAnchor.constraint(equalToConstant: 420)
            wide.priority = .defaultHigh
            constraints.append(wide)
        }
        NSLayoutConstraint.activate(constraints)
    }

    @objc private func tapOutside(_ g: UITapGestureRecognizer) {
        guard let card = view.viewWithTag(1) else { return }
        if !card.frame.contains(g.location(in: view)) { close(PulseInAppOutcome()) }
    }

    private func close(_ outcome: PulseInAppOutcome) {
        self.outcome = outcome
        dismiss(animated: true)
    }

    override func viewDidDisappear(_ animated: Bool) {
        super.viewDidDisappear(animated)
        if isBeingDismissed || presentingViewController == nil { done(outcome) }
    }
}

/// Banner in-app messages, on the key window.
@MainActor
enum InAppBanner {
    static func show(_ m: PulseInAppMessage, in window: UIWindow, done: @escaping (PulseInAppOutcome) -> Void) {
        let c = m.content
        let bg = InAppStyle.color(c.background, .secondarySystemBackground)
        let fg = InAppStyle.color(c.textColor, .label)
        let banner = UIView()
        banner.backgroundColor = bg
        banner.layer.cornerRadius = 12
        banner.layer.shadowOpacity = 0.2
        banner.layer.shadowRadius = 8
        banner.translatesAutoresizingMaskIntoConstraints = false

        var finished = false
        func finish(_ outcome: PulseInAppOutcome) {
            if finished { return }
            finished = true
            UIView.animate(withDuration: 0.2, animations: { banner.alpha = 0 }, completion: { _ in banner.removeFromSuperview() })
            done(outcome)
        }

        let stack = InAppStyle.stack(m, text: fg, accent: fg, buttons: false) { _ in }
        stack.translatesAutoresizingMaskIntoConstraints = false
        banner.addSubview(stack)
        let closeButton = UIButton(type: .system)
        closeButton.setTitle("\u{00D7}", for: .normal)
        closeButton.setTitleColor(fg, for: .normal)
        closeButton.accessibilityLabel = "Close"
        closeButton.translatesAutoresizingMaskIntoConstraints = false
        closeButton.addAction(UIAction { _ in finish(PulseInAppOutcome()) }, for: .touchUpInside)
        banner.addSubview(closeButton)

        let tap = UITapGestureRecognizer()
        banner.addGestureRecognizer(tap)
        tap.addTarget(BannerTapTarget.shared, action: #selector(BannerTapTarget.tapped(_:)))
        BannerTapTarget.shared.handlers[ObjectIdentifier(tap)] = {
            finish(PulseInAppOutcome(button: c.buttons.first { $0.isURL }))
        }

        window.addSubview(banner)
        let g = window.safeAreaLayoutGuide
        let top = c.position == "top"
        NSLayoutConstraint.activate([
            banner.leadingAnchor.constraint(equalTo: g.leadingAnchor, constant: 12),
            banner.trailingAnchor.constraint(equalTo: g.trailingAnchor, constant: -12),
            top ? banner.topAnchor.constraint(equalTo: g.topAnchor, constant: 8)
                : banner.bottomAnchor.constraint(equalTo: g.bottomAnchor, constant: -8),
            stack.topAnchor.constraint(equalTo: banner.topAnchor, constant: 12),
            stack.leadingAnchor.constraint(equalTo: banner.leadingAnchor, constant: 16),
            stack.trailingAnchor.constraint(equalTo: closeButton.leadingAnchor, constant: -8),
            stack.bottomAnchor.constraint(equalTo: banner.bottomAnchor, constant: -12),
            closeButton.topAnchor.constraint(equalTo: banner.topAnchor, constant: 4),
            closeButton.trailingAnchor.constraint(equalTo: banner.trailingAnchor, constant: -8),
        ])
        banner.alpha = 0
        UIView.animate(withDuration: 0.2) { banner.alpha = 1 }
        DispatchQueue.main.asyncAfter(deadline: .now() + 8) {
            BannerTapTarget.shared.handlers[ObjectIdentifier(tap)] = nil
            finish(PulseInAppOutcome(expired: true))
        }
    }
}

/// Target for banner taps (UIKit gesture targets must be objects).
final class BannerTapTarget: NSObject {
    @MainActor static let shared = BannerTapTarget()
    @MainActor var handlers: [ObjectIdentifier: () -> Void] = [:]

    @MainActor @objc func tapped(_ g: UITapGestureRecognizer) {
        handlers.removeValue(forKey: ObjectIdentifier(g))?()
    }
}
#endif
