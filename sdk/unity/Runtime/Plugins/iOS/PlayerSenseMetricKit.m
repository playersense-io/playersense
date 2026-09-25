// MetricKit crash diagnostics, handed to C# as a flat list of lines.
//
// iOS writes a crash diagnostic after the process is gone and delivers it to a subscriber on a
// later launch. No signal handler, no second thread, nothing running while the game runs: the
// subscriber is registered once and the system calls it when it has something.
//
// What leaves this file is only what MetricKit put in the diagnostic: the signal or exception,
// the termination reason and the crashing thread's frames as binary names and offsets. No
// device name, no identifier, nothing about the player. The frames are unsymbolicated, so
// they are the same for every player who hit the same bug, which is what makes them group.

#import <Foundation/Foundation.h>
#import <MetricKit/MetricKit.h>

// At most this many crashes wait for C# to collect them. A launch after a crash loop can
// deliver many; the first few say everything.
static const NSUInteger kMaxPending = 16;
// The crashing thread's innermost frames, as many as the core keeps.
static const NSUInteger kMaxFrames = 128;

static NSMutableArray<NSData *> *s_pending;
static id s_subscriber;

static NSString *Clean(NSString *text)
{
    if (text == nil) return @"";
    NSString *flat = [[text stringByReplacingOccurrencesOfString:@"\t" withString:@" "]
        stringByReplacingOccurrencesOfString:@"\n" withString:@" "];
    return flat.length > 4096 ? [flat substringToIndex:4096] : flat;
}

static NSString *SignalName(NSNumber *signal)
{
    switch (signal.intValue)
    {
        case 4: return @"SIGILL";
        case 5: return @"SIGTRAP";
        case 6: return @"SIGABRT";
        case 7: return @"SIGBUS";
        case 8: return @"SIGFPE";
        case 9: return @"SIGKILL";
        case 11: return @"SIGSEGV";
        default: return nil;
    }
}

static NSString *MachExceptionName(NSNumber *type)
{
    switch (type.intValue)
    {
        case 1: return @"EXC_BAD_ACCESS";
        case 2: return @"EXC_BAD_INSTRUCTION";
        case 3: return @"EXC_ARITHMETIC";
        case 6: return @"EXC_BREAKPOINT";
        case 10: return @"EXC_CRASH";
        case 11: return @"EXC_RESOURCE";
        case 12: return @"EXC_GUARD";
        default: return nil;
    }
}

/// Walks the crashing thread from the JSON MetricKit gives, innermost frame first.
static void AppendFrames(NSMutableString *out, NSData *json, NSString *executable)
{
    NSDictionary *tree = [NSJSONSerialization JSONObjectWithData:json options:0 error:nil];
    NSArray *stacks = [tree isKindOfClass:[NSDictionary class]] ? tree[@"callStacks"] : nil;
    if (![stacks isKindOfClass:[NSArray class]]) return;

    NSDictionary *thread = nil;
    for (NSDictionary *candidate in stacks)
    {
        if ([candidate isKindOfClass:[NSDictionary class]] && [candidate[@"threadAttributed"] boolValue])
        {
            thread = candidate;
            break;
        }
    }
    if (thread == nil) thread = stacks.firstObject;
    if (![thread isKindOfClass:[NSDictionary class]]) return;

    NSArray *roots = thread[@"callStackRootFrames"];
    NSDictionary *frame = [roots isKindOfClass:[NSArray class]] ? roots.firstObject : nil;
    NSUInteger count = 0;
    while ([frame isKindOfClass:[NSDictionary class]] && count < kMaxFrames)
    {
        NSString *binary = frame[@"binaryName"];
        if (![binary isKindOfClass:[NSString class]]) binary = @"?";
        unsigned long long offset = [frame[@"offsetIntoBinaryTextSegment"] unsignedLongLongValue];
        BOOL inApp = [binary isEqualToString:@"UnityFramework"] || [binary isEqualToString:executable];
        [out appendFormat:@"frame\t%@\t0x%llx\t%d\n", Clean(binary), offset, inApp ? 1 : 0];
        count++;

        NSArray *children = frame[@"subFrames"];
        frame = [children isKindOfClass:[NSArray class]] ? children.firstObject : nil;
    }
}

API_AVAILABLE(ios(14.0))
static NSData *Describe(MXCrashDiagnostic *crash, NSDate *at, NSString *executable)
{
    NSString *type = nil;
    NSString *value = crash.terminationReason;

    if (@available(iOS 17.0, *))
    {
        MXCrashDiagnosticObjectiveCExceptionReason *reason = crash.exceptionReason;
        if (reason != nil)
        {
            type = reason.exceptionName;
            value = reason.composedMessage;
        }
    }
    if (type == nil) type = SignalName(crash.signal);
    if (type == nil) type = MachExceptionName(crash.exceptionType);
    if (type == nil) type = @"Crash";

    NSMutableString *out = [NSMutableString string];
    [out appendFormat:@"type\t%@\n", Clean(type)];
    [out appendFormat:@"value\t%@\n", Clean(value.length > 0 ? value : type)];
    [out appendFormat:@"time\t%lld\n", (long long)(at.timeIntervalSince1970 * 1000.0)];
    if (crash.exceptionCode != nil)
        [out appendFormat:@"exit.exception_code\t%@\n", crash.exceptionCode];
    if (crash.virtualMemoryRegionInfo.length > 0)
        [out appendFormat:@"exit.vm_region\t%@\n", Clean(crash.virtualMemoryRegionInfo)];

    NSData *tree = [crash.callStackTree JSONRepresentation];
    if (tree != nil) AppendFrames(out, tree, executable);
    return [out dataUsingEncoding:NSUTF8StringEncoding];
}

API_AVAILABLE(ios(14.0))
@interface PlayerSenseMetricKitSubscriber : NSObject <MXMetricManagerSubscriber>
@end

@implementation PlayerSenseMetricKitSubscriber

- (void)didReceiveDiagnosticPayloads:(NSArray<MXDiagnosticPayload *> *)payloads
{
    NSString *executable = NSBundle.mainBundle.executableURL.lastPathComponent ?: @"";
    for (MXDiagnosticPayload *payload in payloads)
    {
        for (MXCrashDiagnostic *crash in payload.crashDiagnostics)
        {
            NSData *line = Describe(crash, payload.timeStampEnd, executable);
            if (line == nil) continue;
            @synchronized (s_pending)
            {
                if (s_pending.count < kMaxPending) [s_pending addObject:line];
            }
        }
    }
}

- (void)didReceiveMetricPayloads:(NSArray<MXMetricPayload *> *)payloads
{
    // Metrics are not collected. Only crash diagnostics are asked for.
}

@end

int PlayerSense_MetricKit_Available(void)
{
    if (@available(iOS 14.0, *)) return 1;
    return 0;
}

void PlayerSense_MetricKit_Start(void)
{
    if (@available(iOS 14.0, *))
    {
        if (s_subscriber != nil) return;
        s_pending = [[NSMutableArray alloc] init];
        s_subscriber = [PlayerSenseMetricKitSubscriber new];
        [[MXMetricManager sharedManager] addSubscriber:s_subscriber];
    }
}

/// Copies the oldest waiting crash into `buffer` and forgets it. Returns its length, 0 when
/// nothing is waiting, or minus the length needed when `buffer` is too small (nothing is
/// forgotten then).
long long PlayerSense_MetricKit_Next(unsigned char *buffer, long long cap)
{
    if (s_pending == nil) return 0;
    @synchronized (s_pending)
    {
        NSData *next = s_pending.firstObject;
        if (next == nil) return 0;
        long long length = (long long)next.length;
        if (length > cap) return -length;
        memcpy(buffer, next.bytes, (size_t)length);
        [s_pending removeObjectAtIndex:0];
        return length;
    }
}
