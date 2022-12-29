# Clipsync - Sync your Clipboard across machines automatically.

## Description

This program runs as a systemd service (or anything else that puts it in
background) and connects to a Mosquitto ([MQTT](http://mosquitto.org)) broker
of your choice. It then monitors the X clipboard once every two seconds (using
`xclip`) and uploads an encrypted representation of your clipboard to the MQTT
broker on every change. Once that happens, your remote clipsync instances
retrieve the clipboard contents from MQTT, decrypt it and populate the remote
clipboard with your local copy.

Both the X clipboard (Ctrl-C/Ctrl-V) and primary X selection (select with the
mouse, paste using middle mouse button) are synchronized, with the option to
synchronize between the local clipboard and selection automatically.

## Basic principle of operation

* Set you your own MQTT broker or choose a free one.
* Run the program on your local workstation and any remote workstations
  with configuration pointing to the same MQTT broker.
* Work as usual. Any changes to the local clipboard (Ctrl-C) or primary selection
  (selecting with the mouse, pasting with middle click) will populate the remote
  clipboards as well.
* You can also easily send the output of a program to all of your other workstations,
  using `clips copy`.

## Pre-requisites

* A Linux system running X (most distros should be fine.)
* `xclip` installed (under Debian and related systems, run `sudo apt-get install xclip`)
* An account on a private or public MQTT broker (see below).

## Obtaining an MQTT account

There are two basic ways to obtain an MQTT account:

* Create a free account in one of the [many available public brokers](https://mntolia.com/10-free-public-private-mqtt-brokers-for-testing-prototyping/). Please make sure the server you choose supports message retention. Some
don't even require a login and anyone could in theory subscribe to your clipboard. In practice
that's not a problem since the contents of your clipboard are encrypted.

* Set up your own mosquitto broker. A small VM should be sufficient as Mosquitto is
  very modest with resources. Installation of a private broker is outside the scope of
  this documentation, but there are [excellent resources](https://www.digitalocean.com/community/tutorials/how-to-install-and-secure-the-mosquitto-mqtt-messaging-broker-on-debian-10) on the Internet for that.

## Downloading clipsync

The easiest way is to download a binary for your platform directly on the [releases page](https://github.com/marcopaganini/clipsync/releases). Save this binary in a common location like `/usr/local/bin` and
make sure to `chmod 755 /usr/local/bin/clips'

If you have go installed, just clone this repository (`git clone https://github.com/marcopaganini/clipsync`)
and run `make` inside the repo root directory.

## Configuring clipsync

It's possible to specify all configuration items directly on the command-line.
However, this would make it easier for eavesdroppers (with access to your
workstation) to see your user, password, and encryption password. The
recommended way is to create a configuration file under your home directory:

```
mkdir -p ~/.config/clipsync

cat >~/.config/clipsync/config <<EOF
--user=your_user_on_the_remote_mqtt_broker
--server=ssl://your_mqtt_broker_url:<port>
--password-file=~/.config/clipsync/secret
--cryptpass-file=~/.config/clipsync/crypt-password
--redact-level=6
EOF
```

Where:
* `--user`: Specify your username on the remote MQTT broker (or leave blank if no user.)
* `--server`:  The MQTT URL of your server. For SSL encrypted connections, this should be something alone the
  lines of "ssl://mqtt_server.xxx:port". Port 8883 is a common port for SSL enabled MQTT brokers.
* `--password-file`: Is the file containing the MQTT broker password. We'll create this file next.
* `--cryptpass-file:` Is the file containing the password to encrypt the CONTENTS of your clipboard on the remote server. If you run your own server, this is not necessary, but won't hurt. Choose anything large enough. We'll
create this file next
* `--redact-level=6`: Tells clipsync to only log the first and last three characters of your clipboard in the local log.

Now, create the password and encrypted message password files:

```
echo "my_server_password" >~/.config/clipsync/secret
echo "any_large_enough_password_with_666_numbers_and_signs!" >~/.config/clipsync/crypt-password
```

## Test run

To test, just run `clips -v client` and clipsync should connect to your MQTT broker and wait for
clipboard changes. Copy a few items to the clipboard and watch for activity in the log. You can
copy the entire contents of `~/.config/clipsync` to other workstations and run `clips -v client` from
there as well. Clipboards should be synced.

## Production run

The easiest way to run clipsync is by using a user systemd unit. This guarantees that the program will
run automatically on login. To that effect:

* Download the [clipsync.service](https://github.com/marcopaganini/clipsync/blob/master/extras/systemd/clipsync.service) into your user systemd units directory, `~/.config/systemd/user`.
* If you prefer to synchronize your clipboard to your selection, edit the systemd unit file above and change
  the `ExecStart` command to `ExecStart=/usr/local/bin/clips -v client --sync-selections`
* Run `systemctl --user daemon-reload` to reload the configuration.
* Enable and start the unit with `systemctl --user enable clipsync --now`.
* Make sure clipsync was started correctly with `systemctl --user status clipsync`.
* Follow the log with `journalctl --user -u clipsync -f`.

## Tricks and tips

* You can also copy the output of any program to the local and all remote clipboards via command-line by running
  `yourcommand | clips copy`. Running this command on a remote machine will also populate your local clipboard.
* You can paste the clipboard to the standard output using `copy paste`.
* It's possible to configure tmux to send the results of a copy operation to all other clipboards. For that, just
edit your `~/.tmux.conf` file and add:

  ```
  bind-key -T copy-mode-vi Enter send-keys -X copy-pipe-and-cancel "clips copy --filter | xclip -i -f -selection primary | xclip -i -selection clipboard 2>/dev/null"
  ```

  Change `copy-mode-vi` do `copy-mode` if you don't use vi keyboard mapping for your scrollback buffer in tmux.

## Caveats

* Some of the free MQTT servers are not that clear on their use. I plan to find a "recommended" option and
  change this documentation accordingly.
* Clipboard and selection management under X is, frankly, a complete mess. Some programs won't accept the
  primary selection (middle click of the mouse) and require a Ctrl-C/Ctrl-V.
