# Members give the bot commands in their own chat, and a new link's URL goes there

A member can write `/link` to the Telegram bot, supply a name and choose a subscription. The bot creates the link and returns its URL in the same private chat. The member can copy or forward it without opening the web form. See [telegram bot](../processes/platform/telegram-bot.md) and [link lifecycle](../processes/subscriptions/link-lifecycle.md#steps-from-telegram).

## Who is talking: the private chat the member chose

The bot identifies a member by the private chat they selected on Settings → Notifications. Telegram gives a private chat the same ID as its user, so messages there identify that account. The bot ignores commands in groups, including groups used for notifications.

Selecting a chat requires a signed-in session and the member's second factor if enabled. Commands from removed members or members who have not completed setup do not identify an active member.

The bot is a way in without a password or a second step on each command. The commands are kept to what is safe at that strength: each declares an area and a level, the member's grant is read again on every message, and everything runs as `member:<id>` with the member's scope, as a page would. Nothing a command does goes past what the member could do on the page. Commands that delete, rotate or change access stay out of the bot.

## The URL goes to Telegram

The `/link` reply contains the URL once, in the requesting member's private chat. The member can forward or copy it. Telegram stores that message, so anyone who later gains access to the account can read the URL. The URL never enters an event, notification, log line or job payload, and Proxier does not log the reply.

## Long polling, not a webhook

Proxier reads updates with `getUpdates` and a 25-second long poll. A webhook would need a public route for Telegram, and Telegram's addresses reaching home through the gateway; polling needs only the outbound HTTPS Proxier already uses for notifications. Polling confirms each update it reads, so Telegram no longer holds them for **Detect chat**: the bot keeps the chats it heard from (the newest 100) in `telegram_chats`, and Detect chat reads them there.

## Conversations live in memory

Proxier keeps each chat's conversation in memory for 15 minutes, including the current question and offered buttons. A restart discards it and the member starts again. Persisting incomplete conversations would require a table and cleanup without preserving a completed link.
