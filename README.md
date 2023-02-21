# Clipsync - Sync your Clipboard across machines automatically.

## Description

**clipsync** monitors your Linux clipboard (and primary selection) and synchronizes them across multiple computers.

The program requires access to a Mosquitto ([MQTT](http://mosquitto.org)) broker to do its work. If no private MQTT broker is available, **clipsync** connects to a public broker and uses a randomly created channel name. The contents of your clipboard are encrypted to avoid any chances of eavesdropping.

Both the X clipboard (Ctrl-C/Ctrl-V) and primary X selection (select with the mouse, paste using middle mouse button) are synchronized, with the option to synchronize between the local clipboard and primary selection automatically.

## Basic principle of operation

* Set you your own MQTT broker or choose a free one (default if no server specified).
* Run the program on your local workstation running X and any remote workstations with the same set of configuration files (MQTT broker, clipboard encryption password, etc).
* Work as usual. Any changes to the local clipboard (Ctrl-C) or primary selection (selecting with the mouse, pasting with middle click) will populate the remote clipboards as well.
* Remote (or local) servers without X can still use `clipsync copy` and `clipsync paste` to send stdin to a set of remote servers or receive the remote clipboard standard output.
* You can also easily send the output of a program to all of your other workstations, using `clipsync copy`.

## Pre-requisites

* A Linux system running X (most distros should be fine.)
* `xclip` installed (under Debian and related systems, run `sudo apt-get install xclip`)
* An account on a private or public MQTT broker (see below).

## Downloading clipsync

1. Download a binary for your platform directly on the [releases page](https://github.com/marcopaganini/clipsync/releases). Binaries use AppImage, so they should run without effort across Linux distributions.
1. Make the binary executable with `chmod 755 <appimage_name>`.
1. Copy the binary to `/usr/local/bin` with `sudo cp <appimage_name> /usr/local/bin`

Or, if you have go installed, just clone this repository (`git clone https://github.com/marcopaganini/clipsync`)
and run `make` inside the repo root directory.

## Running clipsync

### Using the default MQTT server (no MQTT setup needed)

On your main workstation, just run `clipsync client`. Clipsync will create the initial random password and connect to the default MQTT broker. Note that clipsync will not put itself in the background automatically (more on that below).

To sync with other computers:

* Copy the file `~/.config/clipsync/crypt-password` to the same location on the remote computer.
* If you also run X remotely (E.g, via VNC or something similar), run `clipsync client` there. Anything copied to the local clipboard should also be on the remote clipboard and vice-versa.
* If you don't run X remotely (E.g, access via SSH), you can still use `clipsync copy` and `clipsync paste` to access the synced clipboard.

It's important to make sure that all computers have the same `~/.config/clipsync/crypt-password`, or things won't work as expected.

If all you need is to sync your clipboard, you can safely skip the rest of this section.

### Using a private MQTT server

For more complex configurations, it's possible to specify all configuration items directly on the command-line.  However, this would make it easier for eavesdroppers (with access to your workstation) to see your user, password, and encryption password. The recommended way is to create a configuration file under your home directory:

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

Remember that clipsync will generate a `crypt-password` file automatically the first time it runs, and this file **must be identical on all computers that are synchronizing their clipboards**.

Where:
* `--user`: your username on the remote MQTT broker (or leave blank if no user.)
* `--server`:  the MQTT URL of your broker. For SSL encrypted connections, this should be something along the
  lines of "ssl://mqtt_server.xxx:port". Port 8883 is a common port for SSL enabled MQTT brokers.
* `--password-file`: file containing the MQTT broker password. We'll create this file next.
* `--cryptpass-file:` file containing the password to encrypt the CONTENTS of your clipboard on the remote server.
* `--redact-level=6`: tells clipsync to only log the first and last three characters of your clipboard in the local log.

Now, create the MQTT account password file:

```
echo "my_server_password" >~/.config/clipsync/secret
```

## Obtaining an MQTT account

There are a few ways to go about an MQTT account:

* Use the default mosquitto.org test (public) server. For this to happen, just run `clipsync client` without specifying a server with the `--server` flag.  This directs the program to use clipboard encryption, a random topic and the free servers.  This option is the default and should be zero work. Note that public servers may become unavailable, so YMMV.

* Create a free account in one of the [many available public brokers](https://mntolia.com/10-free-public-private-mqtt-brokers-for-testing-prototyping/). Please make sure the server you choose supports message retention. Some
don't even require a login and anyone could in theory subscribe to your clipboard. In practice
that's not a problem since the contents of your clipboard are encrypted.

* Set up your own mosquitto broker. A small VM or Raspberry Pi should be sufficient as Mosquitto is very modest with resources. Installation of a private broker is outside the scope of this documentation, but there are [excellent resources](https://www.digitalocean.com/community/tutorials/how-to-install-and-secure-the-mosquitto-mqtt-messaging-broker-on-debian-10) on the Internet for that.


## Automating startup using systemd

The easiest way to run clipsync is by using a user systemd unit. This guarantees that the program will
run automatically on login. To do that:

* Download the [clipsync.service](https://github.com/marcopaganini/clipsync/blob/master/extras/systemd/clipsync.service) file into your user systemd units directory, `~/.config/systemd/user`.
* If you prefer to synchronize your clipboard to your selection, edit the systemd unit file above and change
  the `ExecStart` command to `ExecStart=/usr/local/bin/clipsync -v client --sync-selections`
* Run `systemctl --user daemon-reload` to reload the configuration.
* Enable and start the unit with `systemctl --user enable clipsync --now`.
* Make sure clipsync was started correctly with `systemctl --user status clipsync`.
* Follow the log with `journalctl --user -u clipsync -f`.

## Tricks and tips

* You can also copy the output of any program to the local and all remote clipboards via command-line by running
  `yourcommand | clipsync copy`. Running this command on a remote machine will also populate your local clipboard.
* You can paste the clipboard to the standard output using `clipsync paste`.
* It's possible to configure tmux to send the results of a copy operation to all other clipboards. For that, just
edit your `~/.tmux.conf` file and add:

  ```
  bind-key -T copy-mode-vi Enter send-keys -X copy-pipe-and-cancel "clipsync copy --filter | xclip -i -f -selection primary | xclip -i -selection clipboard 2>/dev/null"
  ```

  Change `copy-mode-vi` do `copy-mode` if you don't use vi keyboard mapping for your scrollback buffer in tmux.

## Caveats

* Some of the free MQTT servers are not that clear on their use. I plan to find a "recommended" option and change this documentation accordingly.
* Clipboard and selection management under X is messy. Some programs won't accept the primary selection (middle click of the mouse) and require a Ctrl-C/Ctrl-V. Others (like gnome-terminal) will always update both the primary selection and the clipboard once anything is selected with the mouse.

## Alternatives

* [pbproxy](https://github.com/nikvdp/pbproxy) allows you to send your clipboard anywhere you can ssh to
  using the `pbproxy` command (similar to Mac's pbcopy and pbpaste). pbproxy works with Linux and Macs.
