import Foundation
import PulseCore

#if canImport(UIKit) && !os(watchOS)
import UIKit

/// An app content slot in UIKit: shows the content set in Pulse for slot `slot` (Website & app
/// messages → Content slots, "Where: App").
///
/// With content that has a card, it draws the card's image, title, body and button; a tap on the
/// button (or on a card with a deep link and no button) records `app_content_clicked` and hands the
/// deep link to `PulseConfig.onInAppAction`. Set `render` to draw the content yourself (the only way
/// to show `html` or the card's `data`), and call `click(link:)` from your own buttons. With nothing to
/// show the view is empty and has zero height. `app_content_viewed` is recorded once for each
/// content the view shows while it is on screen.
///
/// The view shows the cached content at once and fetches the slot when it comes on screen (at most
/// every 15 minutes). In a self-sizing table or collection cell, use `onContentChange` to update the
/// row's height.
public final class PulseSlotView: UIView {
    /// The slot id: lowercase letters, digits, `-` and `_`.
    public var slot: String {
        didSet {
            guard slot != oldValue else { return }
            tracked = nil
            apply(PulseContent.isValidSlot(slot) ? client?.content.cached(slot) : nil)
            attach()
        }
    }

    /// Draws the content yourself; return the view to show. Nil (the default) draws the card.
    public var render: ((PulseSlotContent) -> UIView)? {
        didSet { rebuild() }
    }

    /// Called after the content changes (nil: nothing to show), e.g. to reload a table row.
    public var onContentChange: ((PulseSlotContent?) -> Void)?

    /// The slot's current content, or nil.
    public private(set) var content: PulseSlotContent?

    private let explicitClient: PulseClient?
    private var client: PulseClient? { explicitClient ?? PulseClient.shared }
    private var listener: PulseCancellable?
    private var tracked: PulseSlotContent?
    private var shown: UIView?

    /// A view for slot `slot` of `client` (default: `PulseClient.shared`).
    public init(slot: String, client: PulseClient? = nil, render: ((PulseSlotContent) -> UIView)? = nil) {
        self.slot = slot
        self.explicitClient = client
        self.render = render
        super.init(frame: .zero)
    }

    /// From a storyboard: set `slot` in code.
    public required init?(coder: NSCoder) {
        self.slot = ""
        self.explicitClient = nil
        super.init(coder: coder)
    }

    public override var intrinsicContentSize: CGSize {
        CGSize(width: UIView.noIntrinsicMetric, height: shown == nil ? 0 : UIView.noIntrinsicMetric)
    }

    public override func didMoveToWindow() {
        super.didMoveToWindow()
        if window == nil {
            listener?.cancel()
            listener = nil
        } else {
            attach()
        }
    }

    /// Shows the cached content and fetches the slot again if it is older than 15 minutes (done
    /// automatically when the view comes on screen).
    public func reload() { attach() }

    /// Records a tap (`app_content_clicked`) and hands `link` (default: the card's deep link) to
    /// `PulseConfig.onInAppAction`. For the buttons of a custom `render`.
    public func click(link: String? = nil) {
        guard let c = content, let client = client else { return }
        client.content.clicked(slot, c, link: link)
    }

    private func attach() {
        guard window != nil, let client = client, PulseContent.isValidSlot(slot) else { return }
        if listener == nil {
            listener = client.content.addListener { [weak self] changed in
                Task { @MainActor in
                    guard let self = self, changed.contains(self.slot) else { return }
                    self.apply(self.client?.content.cached(self.slot))
                }
            }
        }
        apply(client.content.cached(slot))
        client.content.load([slot])
    }

    private func apply(_ new: PulseSlotContent?) {
        guard new != content else { return trackIfShown() }
        content = new
        rebuild()
        onContentChange?(new)
    }

    private func rebuild() {
        shown?.removeFromSuperview()
        shown = nil
        if let c = content, let v = render.map({ $0(c) }) ?? c.card.map({ cardView($0) }) {
            v.translatesAutoresizingMaskIntoConstraints = false
            addSubview(v)
            NSLayoutConstraint.activate([
                v.topAnchor.constraint(equalTo: topAnchor),
                v.leadingAnchor.constraint(equalTo: leadingAnchor),
                v.trailingAnchor.constraint(equalTo: trailingAnchor),
                v.bottomAnchor.constraint(equalTo: bottomAnchor),
            ])
            shown = v
        }
        invalidateIntrinsicContentSize()
        trackIfShown()
    }

    private func trackIfShown() {
        guard window != nil, shown != nil, let c = content, c != tracked, let client = client else { return }
        tracked = c
        client.content.viewed(slot, c)
    }

    @objc private func cardTapped() {
        if content?.card?.buttonText == nil && content?.card?.deepLink != nil { click() }
    }

    /// Image, title, body and button on a rounded card.
    private func cardView(_ card: PulseSlotCard) -> UIView {
        let box = UIView()
        box.backgroundColor = .secondarySystemBackground
        box.layer.cornerRadius = 12
        box.clipsToBounds = true

        let stack = UIStackView()
        stack.axis = .vertical
        stack.spacing = 8
        stack.translatesAutoresizingMaskIntoConstraints = false
        if card.imageURL != nil {
            let image = UIImageView()
            image.contentMode = .scaleAspectFit
            image.clipsToBounds = true
            image.isHidden = true
            image.heightAnchor.constraint(lessThanOrEqualToConstant: 220).isActive = true
            InAppStyle.loadImage(card.imageURL, into: image)
            stack.addArrangedSubview(image)
        }
        if let title = card.title {
            let l = UILabel()
            l.text = title
            l.numberOfLines = 0
            l.font = .preferredFont(forTextStyle: .headline)
            l.adjustsFontForContentSizeCategory = true
            l.textColor = .label
            stack.addArrangedSubview(l)
        }
        if let body = card.body {
            let l = UILabel()
            l.text = body
            l.numberOfLines = 0
            l.font = .preferredFont(forTextStyle: .subheadline)
            l.adjustsFontForContentSizeCategory = true
            l.textColor = .secondaryLabel
            stack.addArrangedSubview(l)
        }
        if let text = card.buttonText {
            let button = UIButton(type: .system)
            button.setTitle(text, for: .normal)
            button.titleLabel?.font = .preferredFont(forTextStyle: .headline)
            button.backgroundColor = tintColor
            button.setTitleColor(.white, for: .normal)
            button.layer.cornerRadius = 8
            button.contentEdgeInsets = UIEdgeInsets(top: 10, left: 14, bottom: 10, right: 14)
            button.addAction(UIAction { [weak self] _ in self?.click() }, for: .touchUpInside)
            if let last = stack.arrangedSubviews.last { stack.setCustomSpacing(12, after: last) }
            stack.addArrangedSubview(button)
        }
        box.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: box.topAnchor, constant: 16),
            stack.leadingAnchor.constraint(equalTo: box.leadingAnchor, constant: 16),
            stack.trailingAnchor.constraint(equalTo: box.trailingAnchor, constant: -16),
            stack.bottomAnchor.constraint(equalTo: box.bottomAnchor, constant: -16),
        ])
        box.addGestureRecognizer(UITapGestureRecognizer(target: self, action: #selector(cardTapped)))
        return box
    }
}

#if canImport(SwiftUI)
import SwiftUI

/// An app content slot in SwiftUI: `PulseSlot(id: "home_banner")` draws the slot's card (image,
/// title, body, button); `PulseSlot(id: "home_banner") { content in ... }` lets you draw the content
/// yourself (the only way to show `html` or the card's `data`; record taps with
/// `PulseClient.shared?.content.clicked("home_banner", content)`). With nothing to show it takes no
/// space. Records `app_content_viewed` once per content shown, and `app_content_clicked` for taps on
/// the default card's button, whose deep link goes to `PulseConfig.onInAppAction`.
public struct PulseSlot<Custom: View>: View {
    private let id: String
    private let custom: ((PulseSlotContent) -> Custom)?
    @StateObject private var model: PulseSlotModel

    /// Slot `id` of `client` (default: `PulseClient.shared`), drawn by `content`.
    public init(id: String, client: PulseClient? = nil, @ViewBuilder content: @escaping (PulseSlotContent) -> Custom) {
        self.id = id
        self.custom = content
        _model = StateObject(wrappedValue: PulseSlotModel(slot: id, client: client, custom: true))
    }

    public var body: some View {
        VStack(spacing: 0) {
            if let c = model.content, let custom = custom {
                custom(c)
            } else if let c = model.content, let card = c.card {
                PulseSlotCardView(card: card) { model.click() }
            } else {
                Color.clear.frame(width: 0, height: 0)
            }
        }
        .onAppear { model.appear(slot: id) }
        .onDisappear { model.disappear() }
        .onChange(of: id) { model.appear(slot: $0) }
    }
}

extension PulseSlot where Custom == EmptyView {
    /// Slot `id` of `client` (default: `PulseClient.shared`), drawn as a card.
    public init(id: String, client: PulseClient? = nil) {
        self.id = id
        self.custom = nil
        _model = StateObject(wrappedValue: PulseSlotModel(slot: id, client: client, custom: false))
    }
}

/// The state of one `PulseSlot` (used on the main thread).
final class PulseSlotModel: ObservableObject {
    @Published private(set) var content: PulseSlotContent?
    private var slot: String
    private let explicitClient: PulseClient?
    private let custom: Bool
    private var client: PulseClient? { explicitClient ?? PulseClient.shared }
    private var listener: PulseCancellable?
    private var visible = false
    private var tracked: PulseSlotContent?

    init(slot: String, client: PulseClient?, custom: Bool) {
        self.slot = slot
        self.explicitClient = client
        self.custom = custom
    }

    func appear(slot new: String) {
        if new != slot {
            slot = new
            tracked = nil
        }
        visible = true
        guard let client = client, PulseContent.isValidSlot(slot) else {
            update(nil)
            return
        }
        if listener == nil {
            listener = client.content.addListener { [weak self] changed in
                Task { @MainActor in
                    guard let self = self, changed.contains(self.slot) else { return }
                    self.update(self.client?.content.cached(self.slot))
                }
            }
        }
        update(client.content.cached(slot))
        client.content.load([slot])
    }

    func disappear() {
        visible = false
        listener?.cancel()
        listener = nil
    }

    func click() {
        guard let c = content, let client = client else { return }
        client.content.clicked(slot, c)
    }

    private func update(_ c: PulseSlotContent?) {
        if c != content { content = c }
        guard visible, let c = c, custom || c.card != nil, c != tracked, let client = client else { return }
        tracked = c
        client.content.viewed(slot, c)
    }
}

/// The default drawing of a slot's card.
struct PulseSlotCardView: View {
    let card: PulseSlotCard
    let tap: () -> Void
    @StateObject private var image = PulseSlotImage()

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let ui = image.image {
                Image(uiImage: ui)
                    .resizable()
                    .scaledToFit()
                    .frame(maxWidth: .infinity, maxHeight: 220)
            }
            if let title = card.title {
                Text(title).font(.headline).fixedSize(horizontal: false, vertical: true)
            }
            if let text = card.body {
                Text(text).font(.subheadline).foregroundColor(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            if let text = card.buttonText {
                Button(action: tap) {
                    Text(text)
                        .font(.headline)
                        .foregroundColor(.white)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 10)
                        .background(Color.accentColor)
                        .cornerRadius(8)
                }
                .padding(.top, 4)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(UIColor.secondarySystemBackground))
        .cornerRadius(12)
        .contentShape(Rectangle())
        .onTapGesture { if card.buttonText == nil && card.deepLink != nil { tap() } }
        .onAppear { image.load(card.imageURL) }
        .onChange(of: card.imageURL) { image.load($0) }
    }
}

/// Loads a card's image (http/https only).
final class PulseSlotImage: ObservableObject {
    @Published var image: UIImage?
    private var url: String?

    func load(_ s: String?) {
        guard s != url else { return }
        url = s
        image = nil
        guard let s = s, let u = URL(string: s), u.scheme == "https" || u.scheme == "http" else { return }
        URLSession.shared.dataTask(with: u) { [weak self] data, _, _ in
            guard let data = data, let img = UIImage(data: data) else { return }
            DispatchQueue.main.async {
                guard let self = self, self.url == s else { return }
                self.image = img
            }
        }.resume()
    }
}
#endif
#endif
